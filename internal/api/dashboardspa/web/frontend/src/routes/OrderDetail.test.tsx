import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invalidate } from '../api/cache';
import { setActiveCity } from '../api/cityBase';
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
let mockOrdersByName: Record<string, SupervisorOrder> = {};
let mockHistoryByName: Record<string, SupervisorOrderHistoryEntry[]> = {};
let orderMode: 'ok' | 'fail' | 'not-found' | 'pending' = 'ok';
let historyMode: 'ok' | 'fail' | 'pending' = 'ok';
let outputMode: 'ok' | 'pending' = 'ok';
let releaseOrder: (() => void) | null = null;
let releaseHistory: (() => void) | null = null;
let releaseOutput: (() => void) | null = null;
let mockOutput = 'backup completed';

vi.mock('../supervisor/orderReads', () => ({
  getSupervisorOrder: vi.fn(async (name: string) => {
    if (orderMode === 'pending')
      await new Promise<void>((resolve) => {
        releaseOrder = resolve;
      });
    if (orderMode === 'not-found') throw new Error('404 order not found');
    if (orderMode === 'fail' || mockOrder === null) throw new Error('order unavailable');
    return mockOrdersByName[name] ?? mockOrder;
  }),
  listSupervisorOrderHistory: vi.fn(async (name: string) => {
    if (historyMode === 'pending')
      await new Promise<void>((resolve) => {
        releaseHistory = resolve;
      });
    if (historyMode === 'fail') throw new Error('history unavailable');
    return mockHistoryByName[name] ?? mockHistory;
  }),
  getSupervisorOrderHistoryDetail: vi.fn(async (beadId: string, storeRef: string) => {
    if (outputMode === 'pending')
      await new Promise<void>((resolve) => {
        releaseOutput = resolve;
      });
    return {
      bead_id: beadId,
      created_at: '2026-09-23T00:00:00Z',
      labels: null,
      output: mockOutput,
      store_ref: storeRef,
    };
  }),
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

function detailRoute(name = 'triage-sweep') {
  return (
    <MemoryRouter
      initialEntries={[`/orders/${name}`]}
      future={{ v7_relativeSplatPath: true, v7_startTransition: true }}
    >
      <NowProvider>
        <Routes>
          <Route path="/orders/:name" element={<OrderDetailPage />} />
        </Routes>
      </NowProvider>
    </MemoryRouter>
  );
}

function RouteControls() {
  const navigate = useNavigate();
  return (
    <>
      <button onClick={() => navigate('/orders/triage-sweep')}>Order A</button>
      <button onClick={() => navigate('/orders/order-b')}>Order B</button>
    </>
  );
}

function renderWithNavigation() {
  return render(
    <MemoryRouter
      initialEntries={['/orders/triage-sweep']}
      future={{ v7_relativeSplatPath: true, v7_startTransition: true }}
    >
      <NowProvider>
        <RouteControls />
        <Routes>
          <Route path="/orders/:name" element={<OrderDetailPage />} />
        </Routes>
      </NowProvider>
    </MemoryRouter>,
  );
}

function renderDetail(name = 'triage-sweep') {
  return render(detailRoute(name));
}

describe('OrderDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    invalidate('orders');
    setActiveCity('test-city');
    mockOrder = order();
    mockHistory = [];
    mockOrdersByName = {};
    mockHistoryByName = {};
    orderMode = 'ok';
    historyMode = 'ok';
    outputMode = 'ok';
    releaseOrder = null;
    releaseHistory = null;
    releaseOutput = null;
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

  it.each([
    ['triage-sweep/', 'triage-sweep'],
    ['sweep%252Farchive/', 'sweep%2Farchive'],
  ])('fetches the order from a route with a trailing slash: %s', async (route, expected) => {
    renderDetail(route);

    await screen.findByText('Sweep the triage queue.');
    expect(vi.mocked(getSupervisorOrder)).toHaveBeenCalledWith(expected);
    expect(vi.mocked(listSupervisorOrderHistory)).toHaveBeenCalledWith(expected);
  });

  it('keeps a malformed percent escape literal', async () => {
    const warning = vi.spyOn(console, 'warn').mockImplementation((message) => {
      if (!String(message).includes('could not be decoded')) throw new Error(String(message));
    });
    try {
      renderDetail('sweep%broken');

      await screen.findByText('Sweep the triage queue.');
      expect(vi.mocked(getSupervisorOrder)).toHaveBeenCalledWith('sweep%broken');
      expect(warning).toHaveBeenCalled();
    } finally {
      warning.mockRestore();
    }
  });

  it('hides selected output when the active city changes', async () => {
    mockHistory = [historyEntry({ has_output: true })];
    const page = renderDetail();
    await screen.findByText('bd-1');
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));
    expect(await screen.findByText('backup completed')).toBeDefined();

    setActiveCity('other-city');
    page.rerender(detailRoute());

    expect(screen.queryByText('backup completed')).toBeNull();
  });

  it('does not carry output across orders with colliding bead and store IDs', async () => {
    mockHistoryByName = {
      'triage-sweep': [historyEntry({ has_output: true })],
      'order-b': [historyEntry({ name: 'order-b', scoped_name: 'order-b', has_output: true })],
    };
    mockOrdersByName = {
      'order-b': order({ name: 'order-b', scoped_name: 'order-b', description: 'Second order.' }),
    };
    outputMode = 'pending';
    renderWithNavigation();
    await screen.findByText('bd-1');
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));
    expect(await screen.findByText('Loading output.')).toBeDefined();

    fireEvent.click(screen.getByRole('button', { name: 'Order B' }));
    expect(await screen.findByText('Second order.')).toBeDefined();
    await act(async () => {
      releaseOutput?.();
    });
    expect(screen.queryByText('backup completed')).toBeNull();
    expect(screen.getByRole('button', { name: 'View output' })).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Hide output' })).toBeNull();

    outputMode = 'ok';
    mockOutput = 'second order output';
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));
    expect(await screen.findByText('second order output')).toBeDefined();
    fireEvent.click(screen.getByRole('button', { name: 'Order A' }));
    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
    expect(screen.queryByText('second order output')).toBeNull();
    expect(screen.getByRole('button', { name: 'View output' })).toBeDefined();

    outputMode = 'pending';
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));
    expect(screen.queryByText('second order output')).toBeNull();
    mockOutput = 'first order output';
    await act(async () => {
      releaseOutput?.();
    });
    expect(await screen.findByText('first order output')).toBeDefined();
  });

  it('waits for history independently of the order detail', async () => {
    historyMode = 'pending';
    renderDetail();
    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
    expect(screen.getByText('Loading history.')).toBeDefined();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
    await act(async () => {
      releaseHistory?.();
    });
    expect(await screen.findByText('No recorded runs.')).toBeDefined();
  });

  it('does not claim empty history after an independent history failure', async () => {
    historyMode = 'fail';
    renderDetail();
    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
    expect(await screen.findByText('History unavailable.')).toBeDefined();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
    expect(screen.getByRole('alert').textContent).toContain('history unavailable');
  });

  it('withholds cached history after a failed refresh', async () => {
    mockHistory = [historyEntry({ has_output: true })];
    renderDetail();
    expect(await screen.findByText('bd-1')).toBeDefined();
    historyMode = 'fail';
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });
    expect(await screen.findByText('History unavailable.')).toBeDefined();
    expect(screen.queryByText('bd-1')).toBeNull();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
  });

  it('shows completed history while the order definition is loading', async () => {
    orderMode = 'pending';
    renderDetail();
    expect(screen.getAllByText('Loading order.').length).toBeGreaterThan(0);
    expect(await screen.findByText('No recorded runs.')).toBeDefined();
    await act(async () => {
      releaseOrder?.();
    });
    expect(await screen.findByText('Sweep the triage queue.')).toBeDefined();
  });

  it('shows retained history and output when the order definition returns 404', async () => {
    orderMode = 'not-found';
    mockHistory = [
      historyEntry({
        name: 'retired',
        scoped_name: 'retired:rig:old',
        store_ref: 'orders:test-city',
        has_output: true,
      }),
    ];
    renderDetail('retired%3Arig%3Aold');

    expect(
      await screen.findByText('Current order definition unavailable. It may have been removed.'),
    ).toBeDefined();
    expect(await screen.findByText('bd-1')).toBeDefined();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
    expect(screen.queryByText('Status')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'View output' }));
    expect(await screen.findByText('backup completed')).toBeDefined();
    expect(vi.mocked(getSupervisorOrderHistoryDetail)).toHaveBeenCalledWith(
      'bd-1',
      'orders:test-city',
    );
  });

  it('reports both definition and history failures without claiming empty history', async () => {
    orderMode = 'fail';
    historyMode = 'fail';
    renderDetail();

    expect(
      await screen.findByText('Current order definition unavailable. It may have been removed.'),
    ).toBeDefined();
    expect(await screen.findByText('History unavailable.')).toBeDefined();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
    expect(screen.getByRole('alert').textContent).toContain('order unavailable');
    expect(screen.getByRole('alert').textContent).toContain('history unavailable');
  });

  it('shows empty retained history only after a successful read', async () => {
    orderMode = 'not-found';
    historyMode = 'pending';
    renderDetail('retired%3Arig%3Aold');

    expect(
      await screen.findByText('Current order definition unavailable. It may have been removed.'),
    ).toBeDefined();
    expect(screen.getByText('Loading history.')).toBeDefined();
    expect(screen.queryByText('No recorded runs.')).toBeNull();
    await act(async () => {
      releaseHistory?.();
    });
    expect(await screen.findByText('No recorded runs.')).toBeDefined();
  });

  it('renders recent run history with duration and exit code', async () => {
    mockHistory = [
      historyEntry({ outcome: 'success' }),
      historyEntry({
        bead_id: 'bd-2',
        duration_ms: '5000',
        exit_code: '1',
        outcome: 'failed',
      }),
      historyEntry({ bead_id: 'bd-3', exit_code: undefined }),
    ];
    renderDetail();

    expect(await screen.findByText('bd-1')).toBeDefined();
    expect(screen.getAllByText('45.0s')).toHaveLength(2);
    expect(screen.getByText('success')).toBeDefined();
    expect(screen.getByText('failed')).toBeDefined();
    expect(screen.getByText('bd-3').closest('tr')?.textContent).toContain('—');
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
