import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invalidate } from '../api/cache';
import { NowProvider } from '../contexts/NowContext';
import {
  listSupervisorOrderChecks,
  listSupervisorOrders,
  type SupervisorOrder,
  type SupervisorOrderCheck,
} from '../supervisor/orderReads';
import { OrdersPage } from './Orders';

let mockOrders: SupervisorOrder[] = [];
let mockChecks: SupervisorOrderCheck[] = [];
let ordersMode: 'ok' | 'fail' | 'pending' = 'ok';
let checksMode: 'ok' | 'fail' | 'pending' = 'ok';
let releaseOrders: (() => void) | null = null;
let releaseChecks: (() => void) | null = null;

vi.mock('../supervisor/orderReads', () => ({
  listSupervisorOrders: vi.fn(async () => {
    if (ordersMode === 'fail') throw new Error('orders unavailable');
    if (ordersMode === 'pending') {
      await new Promise<void>((resolve) => {
        releaseOrders = resolve;
      });
    }
    return mockOrders;
  }),
  listSupervisorOrderChecks: vi.fn(async () => {
    if (checksMode === 'fail') throw new Error('checks unavailable');
    if (checksMode === 'pending') {
      await new Promise<void>((resolve) => {
        releaseChecks = resolve;
      });
    }
    return mockChecks;
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
    ...overrides,
  };
}

function check(overrides: Partial<SupervisorOrderCheck> = {}): SupervisorOrderCheck {
  return {
    due: false,
    name: 'triage-sweep',
    reason: 'cooldown: 5m remaining',
    scoped_name: 'triage-sweep',
    ...overrides,
  };
}

function renderPage() {
  return render(
    <MemoryRouter future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
      <NowProvider>
        <OrdersPage />
      </NowProvider>
    </MemoryRouter>,
  );
}

describe('OrdersPage', () => {
  beforeEach(() => {
    invalidate('orders');
    mockOrders = [];
    mockChecks = [];
    ordersMode = 'ok';
    checksMode = 'ok';
    releaseOrders = null;
    releaseChecks = null;
  });

  afterEach(() => {
    cleanup();
  });

  it('renders one row per order with trigger, schedule, and scope', async () => {
    mockOrders = [
      order({ trigger: 'interval', interval: '15m' }),
      order({
        name: 'farmer-sweep',
        scoped_name: 'farmer-sweep:rig:demo-repo',
        rig: 'demo-repo',
        trigger: 'schedule',
        schedule: '0 9 * * *',
      }),
    ];
    renderPage();

    expect((await screen.findByRole('link', { name: 'triage-sweep' })).getAttribute('href')).toBe(
      '/orders/triage-sweep',
    );
    expect(screen.getByRole('link', { name: 'farmer-sweep' }).getAttribute('href')).toBe(
      '/orders/farmer-sweep%3Arig%3Ademo-repo',
    );
    expect(screen.getByText('interval')).toBeDefined();
    expect(screen.getByText('15m')).toBeDefined();
    expect(screen.getByText('0 9 * * *')).toBeDefined();
    expect(screen.getByText('city')).toBeDefined();
    expect(screen.getByText('demo-repo')).toBeDefined();
  });

  it('joins check state onto each row: last run plus due badge', async () => {
    mockOrders = [order()];
    mockChecks = [
      check({
        due: true,
        reason: 'elapsed 20m >= interval 15m',
        last_run: new Date(Date.now() - 20 * 60 * 1000).toISOString(),
        last_run_outcome: 'ok',
      }),
    ];
    renderPage();

    expect(await screen.findByText('due now')).toBeDefined();
    expect(screen.getByText(/ago/)).toBeDefined();
    expect(screen.getByText(/ok/)).toBeDefined();
  });

  it('shows the check reason when an order is not due', async () => {
    mockOrders = [order()];
    mockChecks = [check()];
    renderPage();

    expect(await screen.findByText('cooldown: 5m remaining')).toBeDefined();
  });

  it('flags disabled orders', async () => {
    mockOrders = [order({ enabled: false })];
    renderPage();

    expect(await screen.findByText('disabled')).toBeDefined();
    expect(await screen.findByText(/0 due now/)).toBeDefined();
    expect(screen.queryByText('never')).toBeNull();
    expect(listSupervisorOrders).toHaveBeenCalledWith(true);
  });

  it('counts enabled checks while showing disabled orders', async () => {
    mockOrders = [order(), order({ name: 'paused', scoped_name: 'paused', enabled: false })];
    mockChecks = [check({ due: true, reason: 'due' })];
    renderPage();

    expect(await screen.findByText(/1 due now/)).toBeDefined();
    expect(screen.getByText('disabled')).toBeDefined();
    expect(within(screen.getByRole('row', { name: /paused/ })).queryByText('never')).toBeNull();
  });

  it('renders an empty state when no orders are registered', async () => {
    renderPage();

    expect(await screen.findByText('No orders registered.')).toBeDefined();
  });

  it('surfaces fetch errors without crashing', async () => {
    ordersMode = 'fail';
    renderPage();

    expect(await screen.findByRole('alert')).toBeDefined();
  });

  it('refreshes both sources from the Refresh button', async () => {
    mockOrders = [order()];
    renderPage();
    expect(await screen.findByRole('link', { name: 'triage-sweep' })).toBeDefined();
    const callsBefore =
      vi.mocked(listSupervisorOrders).mock.calls.length +
      vi.mocked(listSupervisorOrderChecks).mock.calls.length;

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });

    await waitFor(() => {
      const callsAfter =
        vi.mocked(listSupervisorOrders).mock.calls.length +
        vi.mocked(listSupervisorOrderChecks).mock.calls.length;
      expect(callsAfter).toBeGreaterThan(callsBefore);
    });
  });

  it('withholds due counts while checks are loading', async () => {
    mockOrders = [order()];
    mockChecks = [check()];
    checksMode = 'pending';
    renderPage();

    expect(await screen.findByRole('link', { name: 'triage-sweep' })).toBeDefined();
    expect(screen.getByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/0 due now/)).toBeNull();
    await act(async () => {
      releaseChecks?.();
    });
    expect(await screen.findByText(/0 due now/)).toBeDefined();
  });

  it('withholds stale due counts after a failed refresh', async () => {
    mockOrders = [order()];
    mockChecks = [check({ due: true, reason: 'due now' })];
    renderPage();
    expect(await screen.findByText(/1 due now/)).toBeDefined();

    checksMode = 'fail';
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });

    expect(await screen.findByRole('alert')).toBeDefined();
    expect(screen.getByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/1 due now/)).toBeNull();
    expect(screen.queryByText(/^due now$/)).toBeNull();
  });

  it('withholds due counts when a check has no matching registered order', async () => {
    mockOrders = [order()];
    mockChecks = [
      check(),
      check({ name: 'new-sweep', scoped_name: 'new-sweep', due: true, reason: 'due' }),
    ];
    renderPage();

    expect(await screen.findByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/1 due now/)).toBeNull();
  });

  it('withholds due counts and never-run claims when a new enabled order has no check', async () => {
    mockOrders = [order(), order({ name: 'new-sweep', scoped_name: 'new-sweep' })];
    mockChecks = [check()];
    renderPage();

    expect(await screen.findByRole('link', { name: 'new-sweep' })).toBeDefined();
    expect(screen.getByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/0 due now/)).toBeNull();
    expect(within(screen.getByRole('row', { name: /new-sweep/ })).queryByText('never')).toBeNull();
  });

  it('withholds due counts when an old check remains after an order is removed', async () => {
    mockOrders = [];
    mockChecks = [check({ due: true })];
    renderPage();

    expect(await screen.findByText('No orders registered.')).toBeDefined();
    expect(screen.getByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/1 due now/)).toBeNull();
  });

  it('withholds due counts while orders are loading', async () => {
    mockOrders = [order()];
    mockChecks = [check()];
    ordersMode = 'pending';
    renderPage();

    expect(screen.getAllByText('Loading orders.').length).toBeGreaterThan(0);
    expect(screen.queryByText(/0 due now/)).toBeNull();
    await act(async () => {
      releaseOrders?.();
    });
    expect(await screen.findByText(/0 due now/)).toBeDefined();
  });

  it('withholds counts when the orders refresh fails but checks refresh succeeds', async () => {
    mockOrders = [order()];
    mockChecks = [check()];
    renderPage();
    expect(await screen.findByText(/0 due now/)).toBeDefined();

    ordersMode = 'fail';
    mockChecks = [check({ name: 'new-sweep', scoped_name: 'new-sweep', due: true })];
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    });

    expect(await screen.findByRole('alert')).toBeDefined();
    expect(screen.getByText(/due count unavailable/i)).toBeDefined();
    expect(screen.queryByText(/0 due now/)).toBeNull();
    expect(screen.queryByText(/1 due now/)).toBeNull();
  });
});
