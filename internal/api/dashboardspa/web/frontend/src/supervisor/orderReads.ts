import type {
  OrderCheckResponse,
  OrderHistoryEntry,
  OrderHistoryDetailResponse,
  OrderResponse,
} from 'gas-city-dashboard-shared/gc-supervisor';
import { activeCityOrThrow } from '../api/cityBase';
import { supervisorApi } from './client';

export type SupervisorOrder = OrderResponse;
export type SupervisorOrderCheck = OrderCheckResponse;
export type SupervisorOrderHistoryEntry = OrderHistoryEntry;
export type SupervisorOrderHistoryDetail = OrderHistoryDetailResponse;

export const DEFAULT_ORDER_HISTORY_LIMIT = 20;

export async function listSupervisorOrders(): Promise<SupervisorOrder[]> {
  const list = await supervisorApi().listOrders(activeCityOrThrow('list supervisor orders'));
  return list.orders ?? [];
}

export async function getSupervisorOrder(name: string): Promise<SupervisorOrder> {
  return supervisorApi().getOrder(activeCityOrThrow('get supervisor order'), name);
}

export async function listSupervisorOrderChecks(): Promise<SupervisorOrderCheck[]> {
  const list = await supervisorApi().listOrderChecks(
    activeCityOrThrow('list supervisor order checks'),
  );
  return list.checks ?? [];
}

export async function listSupervisorOrderHistory(
  scopedName: string,
  limit: number = DEFAULT_ORDER_HISTORY_LIMIT,
): Promise<SupervisorOrderHistoryEntry[]> {
  const list = await supervisorApi().orderHistory(
    activeCityOrThrow('list supervisor order history'),
    { scoped_name: scopedName, limit },
  );
  return list.entries ?? [];
}

export async function getSupervisorOrderHistoryDetail(
  beadId: string,
  storeRef: string,
): Promise<SupervisorOrderHistoryDetail> {
  return supervisorApi().orderHistoryDetail(
    activeCityOrThrow('get supervisor order history detail'),
    beadId,
    storeRef,
  );
}
