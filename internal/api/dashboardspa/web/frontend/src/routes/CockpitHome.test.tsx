import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { UsageBody } from 'gas-city-dashboard-shared/gc-supervisor';
import { MemoryRouter } from 'react-router-dom';
import type { RunSummarySubscription } from '../runs/runSummarySubscription';
import { invalidate } from '../api/cache';
import { CockpitHomePage } from './CockpitHome';

const mocks = vi.hoisted(() => ({
  cityUsage: vi.fn(),
  cityStatus: vi.fn(),
  runCensus: vi.fn(),
  listSessions: vi.fn(),
  runSummary: vi.fn(),
}));

vi.mock('../supervisor/client', () => ({
  SUPERVISOR_REQUEST_TIMEOUT_MS: 60_000,
  supervisorApi: () => ({
    cityUsage: mocks.cityUsage,
    cityStatus: mocks.cityStatus,
    runCensus: mocks.runCensus,
    listSessions: mocks.listSessions,
  }),
}));

vi.mock('../runs/runSummarySubscription', () => ({
  useRunSummary: () => mocks.runSummary() as RunSummarySubscription,
}));

const router = (children: React.ReactNode) => (
  <MemoryRouter future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
    {children}
  </MemoryRouter>
);

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function emptyTotals() {
  return {
    invocations: 0,
    compute_facts: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_creation_tokens: 0,
    wall_seconds: 0,
    cost_usd_estimate: 0,
    unpriced: 0,
  };
}

function availableRunSummary(): RunSummarySubscription {
  return {
    loading: false,
    error: null,
    refresh: vi.fn(),
    sseState: 'open',
    source: {
      source: 'runs',
      status: 'available',
      fetchedAt: '2026-07-14T12:00:00Z',
      data: {
        totalActive: 1,
        totalHistorical: 0,
        runCounts: {
          total: 1,
          visible: 1,
          prReview: 0,
          designReview: 0,
          bugfix: 0,
          blocked: 0,
          other: 1,
        },
        lanes: [
          {
            id: 'run-1',
            title: 'Deploy',
            formula: { status: 'known', name: 'deploy' },
            scope: { status: 'unavailable', error: 'scope missing' },
            external: { status: 'unavailable', error: 'external missing' },
            phase: 'active',
            phaseLabel: 'publish',
            statusCounts: {},
            activeAssignees: [],
            updatedAt: { status: 'unavailable', error: 'unknown' },
            stages: Array.from({ length: 7 }, (_, index) => ({
              key: `s${index}`,
              label: `S${index}`,
            })),
            progress: {
              status: 'active_step',
              stage: { status: 'available', index: 6, key: 's6', label: 'publish' },
              attempt: { status: 'available', value: 2 },
            },
            formulaStageResolved: true,
            health: { status: 'unavailable', error: 'not enriched' },
          },
        ],
        historicalLanes: [],
        blockedLanes: [],
        recentChanges: [],
        census: { status: 'unavailable', error: 'not needed' },
      },
    },
  } as unknown as RunSummarySubscription;
}

describe('<CockpitHomePage>', () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  beforeEach(() => {
    invalidate('cockpit:');
    mocks.cityUsage.mockReset().mockResolvedValue({
      available: true,
      recording: true,
      source: 'local_estimate',
      today: {
        invocations: 42,
        compute_facts: 0,
        input_tokens: 1000,
        output_tokens: 200,
        cache_read_tokens: 300,
        cache_creation_tokens: 0,
        wall_seconds: 0,
        cost_usd_estimate: 1.25,
        unpriced: 0,
      },
      last_24h: {
        invocations: 128,
        compute_facts: 0,
        input_tokens: 300000,
        output_tokens: 41000,
        cache_read_tokens: 5000,
        cache_creation_tokens: 0,
        wall_seconds: 0,
        cost_usd_estimate: 3.75,
        unpriced: 0,
      },
      recent: {
        invocations: 3,
        compute_facts: 0,
        input_tokens: 500,
        output_tokens: 100,
        cache_read_tokens: 0,
        cache_creation_tokens: 0,
        wall_seconds: 0,
        cost_usd_estimate: 0.5,
        unpriced: 0,
      },
      recent_by_session: [],
      recent_window_secs: 300,
      updated_at: '2026-07-14T12:00:00Z',
    });
    mocks.cityStatus.mockReset().mockResolvedValue({
      name: 'test-city',
      path: '/tmp/test-city',
      agent_count: 2,
      rig_count: 1,
      running: 2,
      suspended: false,
      uptime_sec: 10,
      agents: { total: 2, running: 2, suspended: 0, quarantined: 0 },
      rigs: { total: 1, suspended: 0 },
      work: { open: 3, ready: 1, in_progress: 1 },
      mail: { total: 2, unread: 0 },
      session_counts_detail: { active: 2, suspended: 0 },
      store_health: {
        live_rows: 10,
        path: '/tmp/store',
        ratio_mb_per_row: 0.1,
        size_bytes: 10,
        threshold_mb_per_row: 1,
        warning: false,
      },
    });
    mocks.runCensus.mockReset().mockResolvedValue({
      runs: [],
      status_counts: {
        pending: 2,
        active: 1,
        waiting: 1,
        canceling: 0,
        completed: 4,
        failed: 0,
        canceled: 0,
        skipped: 0,
      },
    });
    mocks.listSessions.mockReset().mockResolvedValue({
      items: [
        {
          id: 's1',
          session_name: 'worker',
          title: 'Worker',
          provider: 'claude',
          template: 'worker',
          state: 'active',
          attached: false,
          running: true,
          created_at: '2026-07-14T00:00:00Z',
          context_pct: 65,
        },
      ],
      total: 1,
    });
    mocks.runSummary.mockReturnValue(availableRunSummary());
  });

  it('renders real cockpit readings, canonical run states, and variable run progress', async () => {
    render(router(<CockpitHomePage />));
    expect(await screen.findByRole('status', { name: 'model calls today: 42' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'queued: 2' })).toBeTruthy();
    expect(
      screen.getByRole('link', { name: 'deploy: stage 7 of 7, retry attempt 2' }),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Worker: 65% context used' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'live feed: healthy, connected' })).toBeTruthy();
  });

  it('renders the rolling last-24h token, invocation, and cost tiles', async () => {
    render(router(<CockpitHomePage />));
    expect(await screen.findByRole('status', { name: 'tokens in: 300K' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'tokens out: 41K' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'model calls: 128' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'est. cost: $3.75' })).toBeTruthy();
  });

  it('surfaces last-24h totals when today and recent have reset to zero across midnight', async () => {
    // The exact production shape: an idle stretch crossed UTC midnight, so the
    // today (midnight-reset) and recent (300s) windows read zero while the
    // rolling 24h window still holds yesterday's real work ($1.49 / 341k tokens).
    const usage = (await mocks.cityUsage()) as UsageBody;
    const zeroTotals = {
      invocations: 0,
      compute_facts: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 0,
      cost_usd_estimate: 0,
      unpriced: 0,
    };
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today: { ...zeroTotals },
      recent: { ...zeroTotals },
      last_24h: {
        ...zeroTotals,
        invocations: 128,
        input_tokens: 300000,
        output_tokens: 41000,
        cost_usd_estimate: 1.49,
      },
    });

    render(router(<CockpitHomePage />));

    // today is amnesiac after the reset...
    expect(await screen.findByRole('status', { name: 'model calls today: 0' })).toBeTruthy();
    // ...but the rolling 24h window still shows the real numbers.
    expect(screen.getByRole('status', { name: 'tokens in: 300K' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'tokens out: 41K' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'model calls: 128' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'est. cost: $1.49' })).toBeTruthy();
  });

  it('renders per-run tokens/min and dollars/min with an aggregate caption', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today_by_run: [
        {
          run: 'gc-a',
          worker: 'rig/worker-a',
          session_id: 's-a',
          invocations: 2,
          compute_facts: 1,
          input_tokens: 3000,
          output_tokens: 1500,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
          wall_seconds: 90,
          cost_usd_estimate: 0.06,
          unpriced: 0,
          timing_complete: true,
        },
        {
          run: 'gc-b',
          invocations: 1,
          compute_facts: 1,
          input_tokens: 600,
          output_tokens: 300,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
          wall_seconds: 45,
          cost_usd_estimate: 0.09,
          unpriced: 0,
          timing_complete: true,
        },
      ],
      today_by_run_total: 2,
    });

    render(router(<CockpitHomePage />));

    expect(await screen.findByRole('heading', { name: 'run rates · today' })).toBeTruthy();
    // gc-a: 4500 tokens over 90s wall = 3K/min at $0.04/min.
    expect(screen.getByRole('cell', { name: 'gc-a' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: '3K' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: '$0.04' })).toBeTruthy();
    // gc-b: 900 tokens over 45s wall = 1.2K/min at $0.12/min.
    expect(screen.getByRole('cell', { name: 'gc-b' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: '1.2K' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: '$0.12' })).toBeTruthy();
    expect(screen.getByText('aggregate · all 2 runs · 2.4K/min · $0.07/min')).toBeTruthy();
  });

  it('does not present unknown live wall time as zero when model usage exists', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today_by_run: [
        {
          run: 'gc-live',
          invocations: 2,
          compute_facts: 0,
          input_tokens: 5000,
          output_tokens: 1000,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
          wall_seconds: 0,
          cost_usd_estimate: 0.12,
          unpriced: 0,
        },
      ],
    });

    render(router(<CockpitHomePage />));

    const row = (await screen.findByRole('cell', { name: 'gc-live' })).closest('tr');
    expect(row).not.toBeNull();
    expect(
      within(row as HTMLElement)
        .getAllByRole('cell')
        .at(-1)?.textContent,
    ).toBe('—');
    expect(screen.getByText(/usage cannot be tied to a completed measured interval/i)).toBeTruthy();
  });

  it('withholds an aggregate rate when one run has unmeasured wall time', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    const measured = {
      run: 'gc-measured',
      invocations: 1,
      compute_facts: 1,
      input_tokens: 60,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 60,
      cost_usd_estimate: 0.01,
      unpriced: 0,
      timing_complete: true,
    };
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today_by_run: [
        { ...measured, run: 'gc-unknown', input_tokens: 1000, compute_facts: 0, wall_seconds: 0 },
        measured,
      ],
    });
    render(router(<CockpitHomePage />));

    expect(
      await screen.findByText('aggregate · returned 2 runs · full-day scope unknown · — · —'),
    ).toBeTruthy();
    expect(screen.queryByText(/1.1K\/min/)).toBeNull();
  });

  it('withholds per-run rates when model tokens cannot be linked to measured wall time', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today_by_run_total: 1,
      today_by_run: [
        {
          run: 'gc-mixed',
          invocations: 2,
          compute_facts: 1,
          input_tokens: 1060,
          output_tokens: 0,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
          wall_seconds: 60,
          cost_usd_estimate: 0.1,
          unpriced: 0,
        },
      ],
    });
    render(router(<CockpitHomePage />));

    const row = (await screen.findByRole('cell', { name: 'gc-mixed' })).closest('tr');
    expect(row).not.toBeNull();
    expect(
      within(row as HTMLElement)
        .getAllByRole('cell')
        .slice(1, 3)
        .map((cell) => cell.textContent),
    ).toEqual(['—', '—']);
    expect(within(row as HTMLElement).getByRole('cell', { name: '1.0m' })).toBeTruthy();
    expect(screen.getByText('aggregate · all 1 run · — · —')).toBeTruthy();
  });

  it('labels a capped run set and a legacy set without known scope', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    const row = {
      run: 'gc-run',
      invocations: 1,
      compute_facts: 1,
      input_tokens: 60,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 60,
      cost_usd_estimate: 0.01,
      unpriced: 0,
      timing_complete: true,
    };
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today_by_run_total: 25,
      today_by_run: Array.from({ length: 24 }, (_, i) => ({ ...row, run: `gc-${i}` })),
    });
    const capped = render(router(<CockpitHomePage />));
    expect(
      await screen.findByText('aggregate · top 24 of 25 runs · 60/min · $0.01/min'),
    ).toBeTruthy();
    capped.unmount();

    mocks.cityUsage.mockResolvedValue({ ...usage, today_by_run: [row] });
    render(router(<CockpitHomePage />));
    expect(
      await screen.findByText(
        'aggregate · returned 1 run · full-day scope unknown · 60/min · $0.01/min',
      ),
    ).toBeTruthy();
  });

  it('withholds aggregate rates for partial or stale usage readings', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    const measured = {
      run: 'gc-measured',
      invocations: 1,
      compute_facts: 1,
      input_tokens: 60,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      wall_seconds: 60,
      cost_usd_estimate: 0.01,
      unpriced: 0,
      timing_complete: true,
    };
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      partial: true,
      partial_reasons: ['rig unavailable'],
      today_by_run: [measured],
    });
    const partial = render(router(<CockpitHomePage />));
    expect(
      await screen.findByText('aggregate · returned 1 run · full-day scope unknown · — · —'),
    ).toBeTruthy();
    let row = screen.getByRole('cell', { name: 'gc-measured' }).closest('tr');
    expect(
      within(row as HTMLElement)
        .getAllByRole('cell')
        .slice(1, 3)
        .map((cell) => cell.textContent),
    ).toEqual(['—', '—']);
    expect(screen.getByRole('link', { name: 'tokens / min: unavailable' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: unavailable' })).toBeTruthy();
    partial.unmount();

    mocks.cityUsage.mockResolvedValue({ ...usage, partial: false, today_by_run: [measured] });
    const fresh = render(router(<CockpitHomePage />));
    expect(
      await screen.findByText(
        'aggregate · returned 1 run · full-day scope unknown · 60/min · $0.01/min',
      ),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: 'tokens / min: 120' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: $6.00' })).toBeTruthy();
    fresh.unmount();

    mocks.cityUsage.mockRejectedValue(new Error('usage refresh failed'));
    render(router(<CockpitHomePage />));
    expect(
      await screen.findByText('aggregate · returned 1 run · full-day scope unknown · — · —'),
    ).toBeTruthy();
    row = screen.getByRole('cell', { name: 'gc-measured' }).closest('tr');
    expect(
      within(row as HTMLElement)
        .getAllByRole('cell')
        .slice(1, 3)
        .map((cell) => cell.textContent),
    ).toEqual(['—', '—']);
    expect(screen.getByRole('link', { name: 'tokens / min: unavailable' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: unavailable' })).toBeTruthy();
    expect(screen.getAllByText('usage is stale · refresh failed').length).toBeGreaterThan(0);
  });

  it('marks run rates unavailable when the server predates the per-run field', async () => {
    // The default mock carries no today_by_run, matching a server or proxy
    // that predates the field: the section must degrade, never throw.
    render(router(<CockpitHomePage />));

    expect(await screen.findByRole('heading', { name: 'run rates · today' })).toBeTruthy();
    expect(screen.getByText('run rates unavailable')).toBeTruthy();
  });

  it('labels the last-24h tiles unavailable when usage cannot be read', async () => {
    mocks.cityUsage.mockRejectedValue(new Error('usage down'));

    render(router(<CockpitHomePage />));

    await waitFor(() =>
      expect(screen.getByRole('status', { name: 'tokens in: unavailable' })).toBeTruthy(),
    );
    expect(screen.getByRole('status', { name: 'model calls: unavailable' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'est. cost: unavailable' })).toBeTruthy();
  });

  it('renders the whole cockpit when a skewed server or public-front proxy omits last_24h', async () => {
    // Deploy-order safety: the SPA ships in the gc-front shield binary while the
    // API is the supervisor behind a proxy that re-marshals usage without
    // last_24h. A 200 lacking the field must degrade the 24h tiles to
    // unavailable — never throw a render-time TypeError that latches the route
    // ErrorBoundary for the entire cockpit home.
    const usage = (await mocks.cityUsage()) as UsageBody;
    const { last_24h: _omitted, ...withoutLast24h } = usage;
    mocks.cityUsage.mockResolvedValue(withoutLast24h);

    render(router(<CockpitHomePage />));

    // The rest of the cockpit still mounts.
    expect(await screen.findByRole('status', { name: 'model calls today: 42' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'live feed: healthy, connected' })).toBeTruthy();
    // The 24h tiles degrade honestly instead of crashing.
    expect(screen.getByRole('status', { name: 'tokens in: unavailable' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'tokens out: unavailable' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'model calls: unavailable' })).toBeTruthy();
    expect(screen.getByRole('status', { name: 'est. cost: unavailable' })).toBeTruthy();
  });

  it('surfaces the unpriced-cost note when only the 24h window has unpriced calls', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      today: { ...usage.today, unpriced: 0 },
      recent: { ...usage.recent, unpriced: 0 },
      last_24h: { ...usage.last_24h!, unpriced: 3 },
    });

    render(router(<CockpitHomePage />));

    const odometer = await screen.findByRole('status', { name: 'model calls today: 42' });
    expect(odometer.textContent).toContain('cost excludes unpriced model calls');
  });

  it('drives the rate dials off the 24h average when the live 5-minute window is empty', async () => {
    // Facts mint in a burst at session retirement, so the 300s window is empty
    // on a busy pipeline. Production shape: recent 0/0, last_24h 184 calls /
    // 2.65M in / 159k out at $24 — the dials must read the 24h average, not 0.
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      recent: emptyTotals(),
      last_24h: {
        ...emptyTotals(),
        invocations: 184,
        input_tokens: 2_650_000,
        output_tokens: 159_000,
        cost_usd_estimate: 24.0,
      },
    });

    render(router(<CockpitHomePage />));

    // 2,809,000 tokens / 86,400 s * 60 = 1950.69/min -> "2K"; $24 / 24 h -> $1.00/hr.
    expect(await screen.findByRole('link', { name: 'tokens / min: 2K' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: $1.00' })).toBeTruthy();
    // Both dials honestly declare the 24h basis instead of posing as a live rate.
    expect(screen.getAllByText('24 h average')).toHaveLength(2);
  });

  it('keeps the live 5-minute window on the dials when recent has model activity', async () => {
    render(router(<CockpitHomePage />));

    // Default mock: recent 600 tokens / 300 s * 60 = 120/min; $0.50 * 3600/300 = $6.00/hr.
    expect(await screen.findByRole('link', { name: 'tokens / min: 120' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: $6.00' })).toBeTruthy();
    expect(screen.queryByText('24 h average')).toBeNull();
  });

  it('marks the rate dials unavailable when neither the live nor the 24h window has activity', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      recent: emptyTotals(),
      last_24h: emptyTotals(),
    });

    render(router(<CockpitHomePage />));

    expect(await screen.findByRole('link', { name: 'tokens / min: unavailable' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: unavailable' })).toBeTruthy();
  });

  it('marks the rate dials unavailable without crashing when a skewed server omits last_24h', async () => {
    const usage = (await mocks.cityUsage()) as UsageBody;
    const { last_24h: _omitted, ...withoutLast24h } = usage;
    mocks.cityUsage.mockResolvedValue({ ...withoutLast24h, recent: emptyTotals() });

    render(router(<CockpitHomePage />));

    // The cockpit still mounts...
    expect(await screen.findByRole('status', { name: 'model calls today: 42' })).toBeTruthy();
    // ...and the empty-live + absent-24h dials read unavailable rather than a fake 0.
    expect(screen.getByRole('link', { name: 'tokens / min: unavailable' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'burn · $ / hr: unavailable' })).toBeTruthy();
  });

  it('publishes a response slower than the poll cadence without starting an overlapping read', async () => {
    vi.useFakeTimers();
    const initialUsage = (await mocks.cityUsage()) as UsageBody;
    const slowUsage = deferred<UsageBody>();
    mocks.cityUsage
      .mockReset()
      .mockReturnValueOnce(slowUsage.promise)
      .mockResolvedValue(initialUsage);

    render(router(<CockpitHomePage />));
    await act(async () => undefined);
    expect(mocks.cityUsage).toHaveBeenCalledTimes(1);

    await act(() => vi.advanceTimersByTimeAsync(15_001));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(1);

    const publishedUsage = {
      ...initialUsage,
      today: { ...initialUsage.today, invocations: 84 },
      updated_at: '2026-07-14T12:00:16Z',
    };
    await act(async () => {
      slowUsage.resolve(publishedUsage);
      await slowUsage.promise;
    });
    expect(screen.getByRole('status', { name: 'model calls today: 84' })).toBeTruthy();

    await act(() => vi.advanceTimersByTimeAsync(14_999));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(2);
  });

  it('retries after a bounded deadline when a refresh never settles', async () => {
    vi.useFakeTimers();
    const initialUsage = (await mocks.cityUsage()) as UsageBody;
    const recoveredUsage = {
      ...initialUsage,
      today: { ...initialUsage.today, invocations: 126 },
      updated_at: '2026-07-14T12:01:15Z',
    };
    mocks.cityUsage
      .mockReset()
      .mockResolvedValueOnce(initialUsage)
      .mockImplementationOnce(() => new Promise<UsageBody>(() => undefined))
      .mockResolvedValue(recoveredUsage);

    render(router(<CockpitHomePage />));
    await act(async () => undefined);
    expect(screen.getByRole('status', { name: 'model calls today: 42' })).toBeTruthy();

    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(2);

    await act(() => vi.advanceTimersByTimeAsync(59_999));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(2);

    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(3);
    expect(screen.getByRole('status', { name: 'model calls today: 126' })).toBeTruthy();
  });

  it('keeps instruments mounted and labels unavailable sources honestly', async () => {
    mocks.cityUsage.mockRejectedValue(new Error('usage down'));
    mocks.cityStatus.mockRejectedValue(new Error('status down'));
    mocks.runCensus.mockRejectedValue(new Error('runs down'));
    mocks.listSessions.mockRejectedValue(new Error('sessions down'));
    mocks.runSummary.mockReturnValue({
      ...availableRunSummary(),
      source: undefined,
      sseState: 'closed',
    });
    render(router(<CockpitHomePage />));
    await waitFor(() =>
      expect(screen.getByRole('status', { name: 'model calls today: unavailable' })).toBeTruthy(),
    );
    expect(screen.getByTestId('pipeline')).toBeTruthy();
    expect(screen.getByTestId('context-meters')).toBeTruthy();
    expect(screen.getByTestId('run-rings')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'live feed: unknown, disconnected' })).toBeTruthy();
    expect(screen.getAllByText(/unavailable/i).length).toBeGreaterThan(0);
  });

  it('keeps partial usage provenance beside the available odometer reading', async () => {
    const usage = await mocks.cityUsage();
    mocks.cityUsage.mockResolvedValue({
      ...usage,
      partial: true,
      partial_reasons: ['usage history exceeded the dashboard read limit'],
    });

    render(router(<CockpitHomePage />));

    const odometer = await screen.findByRole('status', { name: 'model calls today: 42' });
    expect(odometer.textContent).toContain('usage history exceeded the dashboard read limit');
  });

  it('distinguishes an available historical reading from active recording', async () => {
    const usage = await mocks.cityUsage();
    mocks.cityUsage.mockResolvedValue({ ...usage, recording: false });

    render(router(<CockpitHomePage />));

    const odometer = await screen.findByRole('status', { name: 'model calls today: 42' });
    expect(odometer.textContent).toContain('usage recording is off');
  });

  it('does not present stale status-derived system readings as healthy', async () => {
    const first = render(router(<CockpitHomePage />));
    expect(await screen.findByRole('link', { name: 'dolt store: healthy, healthy' })).toBeTruthy();
    first.unmount();

    mocks.cityStatus.mockRejectedValue(new Error('status refresh failed'));
    render(router(<CockpitHomePage />));

    expect(await screen.findByText('city status is stale · refresh failed')).toBeTruthy();
    expect(
      screen.getByRole('link', {
        name: 'dolt store: unknown, stale · last reported healthy',
      }),
    ).toBeTruthy();
  });

  it('keeps stale status provenance latched while a retry is in flight', async () => {
    const first = render(router(<CockpitHomePage />));
    expect(await screen.findByRole('link', { name: 'dolt store: healthy, healthy' })).toBeTruthy();
    first.unmount();

    vi.useFakeTimers();
    mocks.cityStatus.mockRejectedValueOnce(new Error('status refresh failed'));
    render(router(<CockpitHomePage />));
    await act(async () => undefined);
    expect(screen.getByText('city status is stale · refresh failed')).toBeTruthy();

    mocks.cityStatus.mockImplementationOnce(() => new Promise(() => undefined));
    await act(() => vi.advanceTimersByTimeAsync(15_000));

    expect(screen.getByText('city status is stale · refresh failed')).toBeTruthy();
    expect(
      screen.getByRole('link', {
        name: 'dolt store: unknown, stale · last reported healthy',
      }),
    ).toBeTruthy();
  });

  it('uses the session list when detailed active-session counts are absent', async () => {
    const current = await mocks.cityStatus();
    const { session_counts_detail: _detail, ...withoutSessionCounts } = current;
    mocks.cityStatus.mockResolvedValue({ ...withoutSessionCounts, running: 99 });

    render(router(<CockpitHomePage />));

    expect(await screen.findByRole('link', { name: 'active sessions: 1' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'active sessions: 99' })).toBeNull();
  });

  it('reports failed store maintenance when the size ratio is within threshold', async () => {
    const current = await mocks.cityStatus();
    mocks.cityStatus.mockResolvedValue({
      ...current,
      store_health: { ...current.store_health, last_gc_status: 'failed', warning: false },
    });

    render(router(<CockpitHomePage />));

    expect(
      await screen.findByRole('link', {
        name: 'dolt store: warning, maintenance failed',
      }),
    ).toBeTruthy();
  });

  it('marks partial status lamps unknown and reports failed store maintenance', async () => {
    const current = await mocks.cityStatus();
    mocks.cityStatus.mockResolvedValue({
      ...current,
      partial: true,
      store_health: { ...current.store_health, last_gc_status: 'failed' },
    });

    render(router(<CockpitHomePage />));

    expect(
      await screen.findByRole('link', {
        name: 'dolt store: unknown, partial · last reported maintenance failed',
      }),
    ).toBeTruthy();
  });

  it('freezes live instrument projections while paused', async () => {
    vi.useFakeTimers();
    const view = render(router(<CockpitHomePage />));
    await act(async () => undefined);
    expect(screen.getByRole('link', { name: 'live feed: healthy, connected' })).toBeTruthy();
    expect(mocks.cityUsage).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'pause instruments' }));
    mocks.runSummary.mockReturnValue({ ...availableRunSummary(), sseState: 'closed' });
    view.rerender(router(<CockpitHomePage />));

    await act(() => vi.advanceTimersByTimeAsync(120_000));

    expect(screen.getByRole('button', { name: 'resume instruments' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'live feed: healthy, connected' })).toBeTruthy();
    expect(mocks.cityUsage).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'resume instruments' }));
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(mocks.cityUsage).toHaveBeenCalledTimes(2);
  });
});
