import { describe, expect, it } from 'vitest';
import type { RunLane } from 'gas-city-dashboard-shared';
import type { UsageRunToday } from 'gas-city-dashboard-shared/gc-supervisor';
import {
  aggregateRunRates,
  burnPerHour,
  dollarsPerMinute,
  laneToRing,
  pipelineSegments,
  pipelineWidths,
  runRateAvailable,
  tokensPerMinute,
} from './model';

describe('cockpit telemetry derivation', () => {
  it('normalizes segment floors to exactly 100 percent', () => {
    const widths = pipelineWidths([100, 0, Number.NaN, -4]);
    expect(widths.reduce((sum, width) => sum + width, 0)).toBeCloseTo(100, 8);
    expect(widths.every((width) => Number.isFinite(width) && width >= 0)).toBe(true);
    expect(pipelineWidths([0, 0, 0, 0])).toEqual([25, 25, 25, 25]);
  });

  it('uses only canonical nonterminal run states', () => {
    expect(
      pipelineSegments({
        pending: 2,
        active: 3,
        waiting: 1,
        canceling: 4,
        completed: 99,
        failed: 8,
        canceled: 7,
        skipped: 6,
      }).map(({ key, count }) => [key, count]),
    ).toEqual([
      ['pending', 2],
      ['active', 3],
      ['waiting', 1],
      ['canceling', 4],
    ]);
  });

  it('derives bounded rates and rejects invalid inputs', () => {
    const totals = {
      invocations: 1,
      compute_facts: 0,
      input_tokens: 100,
      output_tokens: 20,
      cache_read_tokens: 30,
      cache_creation_tokens: 10,
      wall_seconds: 0,
      cost_usd_estimate: 0.5,
      unpriced: 0,
    };
    expect(tokensPerMinute(totals, 300)).toBe(32);
    expect(burnPerHour(totals, 300)).toBe(6);
    expect(tokensPerMinute({ ...totals, input_tokens: Number.NaN }, 0)).toBeNull();
    expect(burnPerHour({ ...totals, cost_usd_estimate: Number.MAX_VALUE }, 1)).toBeNull();
    expect(
      tokensPerMinute(
        {
          ...totals,
          input_tokens: Number.MAX_VALUE,
          output_tokens: 0,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
        },
        Number.MIN_VALUE,
      ),
    ).toBeNull();
  });

  it('derives dollars per minute on the same basis as burn per hour', () => {
    const totals = {
      invocations: 1,
      compute_facts: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 0,
      cost_usd_estimate: 3,
      unpriced: 0,
    };
    expect(dollarsPerMinute(totals, 60)).toBe(3);
    expect(dollarsPerMinute(totals, 30)).toBe(6);
    expect(dollarsPerMinute(totals, 0)).toBeNull();
    expect(dollarsPerMinute({ ...totals, cost_usd_estimate: -1 }, 60)).toBeNull();
  });

  it('aggregates per-run rows into combined rates over summed wall-clock', () => {
    const row = (overrides: Partial<UsageRunToday>): UsageRunToday => ({
      run: 'gc-1',
      invocations: 1,
      compute_facts: 1,
      input_tokens: 1200,
      output_tokens: 600,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 60,
      cost_usd_estimate: 0.06,
      unpriced: 0,
      timing_complete: true,
      ...overrides,
    });
    expect(aggregateRunRates([])).toBeNull();
    expect(aggregateRunRates([row({}), row({ run: 'gc-2' })])).toEqual({
      tokensPerMinute: 1800,
      dollarsPerMinute: 0.06,
      runs: 2,
      timingUnknown: false,
    });
    const noWall = aggregateRunRates([row({ wall_seconds: 0 })]);
    expect(noWall?.runs).toBe(1);
    expect(noWall?.tokensPerMinute).toBeNull();
    expect(noWall?.dollarsPerMinute).toBeNull();
    expect(noWall?.timingUnknown).toBe(true);

    for (const rows of [
      [
        row({ input_tokens: 1000, compute_facts: 0, wall_seconds: 0 }),
        row({ run: 'gc-2', input_tokens: 60 }),
      ],
      [row({ compute_facts: 0, wall_seconds: 0 })],
      [row({ wall_seconds: null as unknown as number })],
      [row({ timing_complete: false })],
      [row({ timing_complete: undefined })],
    ]) {
      expect(aggregateRunRates(rows)).toMatchObject({
        tokensPerMinute: null,
        dollarsPerMinute: null,
        timingUnknown: true,
      });
    }
    expect(runRateAvailable(row({}))).toBe(true);
    expect(runRateAvailable(row({ timing_complete: undefined }))).toBe(false);
  });

  it('carries each lane real stage total and retry provenance', () => {
    const lane = {
      id: 'run-1',
      title: 'Seven-stage run',
      formula: { status: 'known', name: 'deploy' },
      scope: { status: 'unavailable', error: 'not resolved' },
      phase: 'active',
      phaseLabel: 'publish',
      stages: Array.from({ length: 7 }, (_, index) => ({ key: `s${index}`, label: `S${index}` })),
      progress: {
        status: 'active_step',
        stage: { status: 'available', index: 6, key: 's6', label: 'S6' },
        attempt: { status: 'available', value: 2 },
      },
    } as unknown as RunLane;
    expect(laneToRing(lane)).toMatchObject({ stage: 7, totalStages: 7, attempt: 2 });
  });
});
