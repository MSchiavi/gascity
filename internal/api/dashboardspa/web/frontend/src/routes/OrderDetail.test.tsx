import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invalidate } from '../api/cache';
import { NowProvider } from '../contexts/NowContext';
import {
  getSupervisorOrder,
  getSupervisorOrderHistoryDetail,
  listSupervisorOrderHistory,
  type SupervisorOrder,
  type SupervisorOrderHistoryEntry,
} from '../supervisor/orderReads';
import { OrderDetailPage } from './OrderDetail';

let mockOrder: SupervisorOrder | null = null;
let mockHistory: SupervisorOrderHistoryEntry[] = [];
let orderMode: 'ok' | 'fail' = 'ok';
let mockOutput = 'backup completed';

vi.mock('../supervisor/orderReads', () => ({
  getSupervisorOrder: vi.fn(async () => {
    if (orderMode === 'fail' || mockOrder === null) throw new Error('order unavailable');
    return mockOrder;
  }),
  listSupervisorOrderHistory: vi.fn(async () => mockHistory),
  getSupervisorOrderHistoryDetail: vi.fn(async (beadId: string, storeRef: string) => ({
    bead_id: beadId,
    created_at: '2026-09-23T00:00:00Z',
    labels: null,
    output: mockOutput,
    store_ref: storeRef,
  })),
}));

function order(overrides: Partial<SupervisorOrder> = {}): SupervisorOrder {
  return {
    capture_output: false,
    enabled: true,
    name: 'triage-sweep',
    scoped_name: 'triage-sweep',
    timeout_ms: 60000,
    type: 'agent',
    trigger: 'interval',
    interval: '15m',
    description: 'Sweep the triage queue.',
    ...overrides,
  };
}

function historyEntry(
  overrides: Partial<SupervisorOrderHistoryEntry> = {},
): SupervisorOrderHistoryEntry {
  return {
    bead_id: 'bd-1',
    capture_output: false,
    created_at: new Date(Date.now() - 20 * 60 * 1000).toISOString(),
    has_output: false,
    labels: null,
    name: 'triage-sweep',
    scoped_name: 'triage-sweep',
    store_ref: 'city:test-city',
    duration_ms: '45000',
    exit_code: '0',
    ...overrides,
  };
}

function renderDetail(name = 'triage-sweep') {
  return render(
    <MemoryRouter
      initialEntries={[`/orders/${name}`]}
      future={{ v7_relativeSplatPath: true, v7_startTransition: true }}
    >
      <NowProvider>
        <Routes>
          <Route path="/orders/:name" element={<OrderDetailPage />} />
        </Routes>
      </NowProvider>
    </MemoryRouter>,
  );
}

describe('OrderDetailPage', () => {
  beforeEach(() => {
    invalidate('orders');
    mockOrder = order();
    mockHistory = [];
    orderMode = 'ok';
    mockOutput = 'backup completed';
  });

  afterEach(() => {
    cleanup();
  });

  it('renders the description, exec/formula, and trigger config', async () => {
    mockOrder = order({ formula: 'mol-triage-sweep' });
    renderDetail();

    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
    expect(screen.getByText('mol-triage-sweep')).toBeDefined();
    expect(screen.getByText('interval')).toBeDefined();
    expect(screen.getByText('15m')).toBeDefined();
  });

  it('renders the exec command when the order has no formula', async () => {
    mockOrder = order({ exec: 'bd ready' });
    renderDetail();

    expect(await screen.findByText('bd ready')).toBeDefined();
  });

  it('decodes a scoped route param before fetching', async () => {
    renderDetail('farmer-sweep%3Arig%3Ademo-repo');

    await screen.findByText('Sweep the triage queue.');
    expect(vi.mocked(getSupervisorOrder)).toHaveBeenCalledWith('farmer-sweep:rig:demo-repo');
    expect(vi.mocked(listSupervisorOrderHistory)).toHaveBeenCalledWith(
      'farmer-sweep:rig:demo-repo',
    );
  });

  it('preserves percent escapes in a decoded order name', async () => {
    renderDetail('sweep%252Farchive');

    await screen.findByText('Sweep the triage queue.');
    expect(vi.mocked(getSupervisorOrder)).toHaveBeenCalledWith('sweep%2Farchive');
    expect(vi.mocked(listSupervisorOrderHistory)).toHaveBeenCalledWith('sweep%2Farchive');
  });

  it('renders recent run history with duration and exit code', async () => {
    mockHistory = [
      historyEntry(),
      historyEntry({
        bead_id: 'bd-2',
        duration_ms: '5000',
        exit_code: '1',
        error: 'check timed out',
      }),
    ];
    renderDetail();

    expect(await screen.findByText('bd-1')).toBeDefined();
    expect(screen.getByText('45.0s')).toBeDefined();
    expect(screen.getByText('check timed out')).toBeDefined();
  });

  it('renders an empty history state', async () => {
    renderDetail();

    expect(await screen.findByText('No recorded runs.')).toBeDefined();
  });

  it('loads stored output only when a run offers it', async () => {
    mockHistory = [historyEntry({ has_output: true }), historyEntry({ bead_id: 'bd-2' })];
    renderDetail();

    expect(await screen.findByText('bd-1')).toBeDefined();
    expect(screen.getAllByRole('button', { name: 'View output' })).toHaveLength(1);
    expect(vi.mocked(getSupervisorOrderHistoryDetail)).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));

    expect(await screen.findByText('backup completed')).toBeDefined();
    expect(getSupervisorOrderHistoryDetail).toHaveBeenCalledWith('bd-1', 'city:test-city');
    fireEvent.click(screen.getByRole('button', { name: 'Hide output' }));
    expect(screen.queryByText('backup completed')).toBeNull();
  });

  it('surfaces fetch errors without crashing', async () => {
    orderMode = 'fail';
    renderDetail();

    expect(await screen.findByRole('alert')).toBeDefined();
  });

  it('refreshes from the Refresh button', async () => {
    renderDetail();
    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
    const callsBefore = vi.mocked(getSupervisorOrder).mock.calls.length;

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });

    await waitFor(() => {
      expect(vi.mocked(getSupervisorOrder).mock.calls.length).toBeGreaterThan(callsBefore);
    });
  });
});
