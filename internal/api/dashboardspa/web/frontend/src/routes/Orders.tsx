import { useCallback, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../components/Button';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/StatusBadge';
import { Table, type TableColumn } from '../components/Table';
import { useNow } from '../contexts/NowContext';
import { formatRelative } from '../hooks/time';
import { useCachedData } from '../hooks/useCachedData';
import { useVisibleRefresh } from '../hooks/useVisibleRefresh';
import { getActiveCity } from '../api/cityBase';
import {
  listSupervisorOrderChecks,
  listSupervisorOrders,
  type SupervisorOrder,
  type SupervisorOrderCheck,
} from '../supervisor/orderReads';

// /orders route: read-only list of registered orders — city-local
// (~/gc/orders/*.toml) and pack-bundled — with trigger config, scope,
// and live last-run / due state joined from /orders/check keyed by
// scoped_name. Enabling, disabling, or editing orders stays out of the
// UI; the read-only view links each row to /orders/:name for detail.

interface OrderRow {
  order: SupervisorOrder;
  check: SupervisorOrderCheck | null;
}

export function OrdersPage() {
  const now = useNow();
  const city = getActiveCity();
  const ordersSource = useCachedData(`orders:list:${city ?? 'no-city'}`, () => listSupervisorOrders());
  const checksSource = useCachedData(`orders:checks:${city ?? 'no-city'}`, () => listSupervisorOrderChecks());

  const refresh = useCallback(async () => {
    await Promise.all([ordersSource.refresh(), checksSource.refresh()]);
  }, [ordersSource, checksSource]);
  useVisibleRefresh(refresh, 30_000);

  const rows = useMemo<OrderRow[]>(() => {
    const checks = new Map(checksSource.data?.map((c) => [c.scoped_name, c]) ?? []);
    return (ordersSource.data ?? []).map((order) => ({
      order,
      check: checks.get(order.scoped_name) ?? null,
    }));
  }, [ordersSource.data, checksSource.data]);

  const dueCount = rows.filter((row) => row.check?.due === true).length;
  const loading = ordersSource.loading || checksSource.loading;
  const error =
    [ordersSource.error, checksSource.error]
      .filter((value): value is string => value !== null)
      .join('; ') || null;

  const synopsis =
    ordersSource.data === undefined
      ? 'Loading orders.'
      : `${rows.length} ${rows.length === 1 ? 'order' : 'orders'} registered. ${dueCount} due now.`;

  return (
    <section>
      <PageHeader
        title="Orders"
        synopsis={synopsis}
        meta={
          <>
            {error && (
              <span className="normal-case text-body text-accent" role="alert">
                {error}
              </span>
            )}
            <Button
              size="sm"
              className="w-full justify-center"
              onClick={() => void refresh()}
              disabled={loading}
            >
              {loading ? 'Refreshing' : 'Refresh'}
            </Button>
          </>
        }
      />

      {ordersSource.data === undefined && error === null ? (
        <p className="text-body text-fg-muted italic">Loading orders.</p>
      ) : (
        <Table columns={orderColumns(now)} rows={rows} rowKey={orderRowKey} empty="No orders registered." />
      )}
    </section>
  );
}

function orderRowKey(row: OrderRow): string {
  return row.order.scoped_name;
}

function orderColumns(now: number): ReadonlyArray<TableColumn<OrderRow>> {
  return [
    {
      key: 'order',
      label: 'Order',
      sortable: true,
      sortValue: (row) => row.order.name,
      render: (row) => (
        <span className="inline-flex items-baseline gap-2">
          <Link
            to={`/orders/${encodeURIComponent(row.order.scoped_name)}`}
            className="font-medium text-fg hover:underline"
          >
            {row.order.name}
          </Link>
          {!row.order.enabled && <StatusBadge tone="neutral" label="disabled" />}
        </span>
      ),
    },
    {
      key: 'trigger',
      label: 'Trigger',
      render: (row) => (
        <span>
          {row.order.trigger ?? '—'}
          {row.order.on !== undefined && (
            <span className="block text-fg-muted">on {row.order.on}</span>
          )}
        </span>
      ),
    },
    {
      key: 'schedule',
      label: 'Interval / Schedule',
      render: (row) => <span>{row.order.interval ?? row.order.schedule ?? '—'}</span>,
    },
    {
      key: 'scope',
      label: 'Scope',
      render: (row) => <span>{row.order.rig ?? 'city'}</span>,
    },
    {
      key: 'last-run',
      label: 'Last run',
      sortable: true,
      sortValue: (row) => row.check?.last_run ?? null,
      render: (row) =>
        row.check?.last_run === undefined ? (
          <span className="text-fg-muted">never</span>
        ) : (
          <span>
            {formatRelative(row.check.last_run, now)} ago
            {row.check.last_run_outcome !== undefined && (
              <span className="text-fg-muted"> · {row.check.last_run_outcome}</span>
            )}
          </span>
        ),
    },
    {
      key: 'next-due',
      label: 'Next due',
      render: (row) =>
        row.check === null ? (
          <span className="text-fg-muted">—</span>
        ) : row.check.due ? (
          <StatusBadge tone="warn" label="due now" title={row.check.reason} />
        ) : (
          <span className="text-fg-muted">{row.check.reason}</span>
        ),
    },
  ];
}
