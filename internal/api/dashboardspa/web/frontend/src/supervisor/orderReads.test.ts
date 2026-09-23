import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { OrderCheckListBody } from 'gas-city-dashboard-shared/gc-supervisor';
import { setActiveCity } from '../api/cityBase';
import { resetSupervisorApiForTests, setSupervisorApiForTests, type SupervisorApi } from './client';
import {
  DEFAULT_ORDER_HISTORY_LIMIT,
  getSupervisorOrder,
  getSupervisorOrderHistoryDetail,
  listSupervisorOrderChecks,
  listSupervisorOrderHistory,
  listSupervisorOrders,
} from './orderReads';

const baseApi: SupervisorApi = {
  baseUrl: '/gc-supervisor',
  health: vi.fn(),
  cityHealth: vi.fn(),
  cityStatus: vi.fn(),
  cityUsage: vi.fn(),
  runCensus: vi.fn(),
  listCities: vi.fn(),
  listAgents: vi.fn(),
  listRigs: vi.fn(),
  listBeads: vi.fn(),
  listEvents: vi.fn(),
  getBead: vi.fn(),
  createBead: vi.fn(),
  updateBead: vi.fn(),
  closeBead: vi.fn(),
  sling: vi.fn(),
  formulaFeed: vi.fn(),
  listMail: vi.fn(),
  markMailRead: vi.fn(),
  markMailUnread: vi.fn(),
  archiveMail: vi.fn(),
  replyMail: vi.fn(),
  sendMail: vi.fn(),
  mailThread: vi.fn(),
  cityEventStreamUrl: vi.fn(),
  sessionStreamUrl: vi.fn(),
  listSessions: vi.fn(),
  sessionPending: vi.fn(),
  respondSession: vi.fn(),
  sessionTranscript: vi.fn(),
  workflowRun: vi.fn(),
  formulaDetail: vi.fn(),
  listOrders: vi.fn(),
  getOrder: vi.fn(),
  listOrderChecks: vi.fn(),
  orderHistory: vi.fn(),
  orderHistoryDetail: vi.fn(),
  mutationHeaders: () => ({ 'X-GC-Request': 'dashboard' }),
};

function order(overrides = {}) {
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

describe('supervisor order reads', () => {
  beforeEach(() => {
    setActiveCity('test-city');
  });

  afterEach(() => {
    resetSupervisorApiForTests();
  });

  it('lists supervisor orders against the active city', async () => {
    const listOrders = vi.fn(async () => ({ orders: [order()] }));
    setSupervisorApiForTests({ ...baseApi, listOrders });

    const result = await listSupervisorOrders();

    expect(listOrders).toHaveBeenCalledWith('test-city', undefined);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ name: 'triage-sweep' });
  });

  it('requests disabled orders only for the registered-orders view', async () => {
    const listOrders = vi.fn(async () => ({ orders: [order({ enabled: false })] }));
    setSupervisorApiForTests({ ...baseApi, listOrders });

    const result = await listSupervisorOrders(true);

    expect(listOrders).toHaveBeenCalledWith('test-city', { include_disabled: true });
    expect(result[0]?.enabled).toBe(false);
  });

  it('normalizes a null orders list to an empty array', async () => {
    const listOrders = vi.fn(async () => ({ orders: null }));
    setSupervisorApiForTests({ ...baseApi, listOrders });

    await expect(listSupervisorOrders()).resolves.toEqual([]);
  });

  it('fetches one supervisor order by name', async () => {
    const getOrder = vi.fn(async () => order({ description: 'sweep triage' }));
    setSupervisorApiForTests({ ...baseApi, getOrder });

    const result = await getSupervisorOrder('triage-sweep');

    expect(getOrder).toHaveBeenCalledWith('test-city', 'triage-sweep');
    expect(result).toMatchObject({ description: 'sweep triage' });
  });

  it('lists supervisor order checks and normalizes null to an empty array', async () => {
    const listOrderChecks = vi.fn(
      async (): Promise<OrderCheckListBody> => ({
        checks: [
          { due: true, name: 'triage-sweep', reason: 'never run', scoped_name: 'triage-sweep' },
        ],
      }),
    );
    setSupervisorApiForTests({ ...baseApi, listOrderChecks });

    const result = await listSupervisorOrderChecks();

    expect(listOrderChecks).toHaveBeenCalledWith('test-city');
    expect(result).toHaveLength(1);

    listOrderChecks.mockResolvedValue({ checks: null });
    await expect(listSupervisorOrderChecks()).resolves.toEqual([]);
  });

  it('fetches supervisor order history with the default limit', async () => {
    const orderHistory = vi.fn(async () => ({ entries: [] }));
    setSupervisorApiForTests({ ...baseApi, orderHistory });

    await listSupervisorOrderHistory('triage-sweep');

    expect(orderHistory).toHaveBeenCalledWith('test-city', {
      scoped_name: 'triage-sweep',
      limit: DEFAULT_ORDER_HISTORY_LIMIT,
    });
  });

  it('normalizes null history entries to an empty array', async () => {
    const orderHistory = vi.fn(async () => ({ entries: null }));
    setSupervisorApiForTests({ ...baseApi, orderHistory });

    await expect(listSupervisorOrderHistory('triage-sweep')).resolves.toEqual([]);
  });

  it('fetches stored run output from the exact history store', async () => {
    const detail = {
      bead_id: 'bd-1',
      created_at: '2026-09-23T00:00:00Z',
      labels: null,
      output: 'backup completed',
      store_ref: 'city:test-city',
    };
    const orderHistoryDetail = vi.fn(async () => detail);
    setSupervisorApiForTests({ ...baseApi, orderHistoryDetail });

    await expect(getSupervisorOrderHistoryDetail('bd-1', 'city:test-city')).resolves.toEqual(
      detail,
    );
    expect(orderHistoryDetail).toHaveBeenCalledWith('test-city', 'bd-1', 'city:test-city');
  });
});
