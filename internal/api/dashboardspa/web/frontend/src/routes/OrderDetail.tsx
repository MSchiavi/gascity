import { useCallback, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { Button } from '../components/Button';
import { Field } from '../components/Field';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/StatusBadge';
import { Table, type TableColumn } from '../components/Table';
import { useNow } from '../contexts/NowContext';
import { formatRelative } from '../hooks/time';
import { useCachedData } from '../hooks/useCachedData';
import { useVisibleRefresh } from '../hooks/useVisibleRefresh';
import { getActiveCity } from '../api/cityBase';
import {
  getSupervisorOrder,
  getSupervisorOrderHistoryDetail,
  listSupervisorOrderHistory,
  type SupervisorOrderHistoryEntry,
} from '../supervisor/orderReads';

// /orders/:name route: read-only drilldown for one order. The :name param
// carries the scoped name (URL-encoded); the backend resolves both plain
// and scoped names. Shows the description, exec/formula source, trigger
// config, and recent run history.

export function OrderDetailPage() {
  const { pathname } = useLocation();
  const routePath = pathname.replace(/\/+$/, '');
  const rawName = routePath.slice(routePath.lastIndexOf('/') + 1);
  let scopedName: string;
  try {
    scopedName = decodeURIComponent(rawName);
  } catch {
    scopedName = rawName;
  }
  const city = getActiveCity();
  return (
    <OrderDetailContent
      key={JSON.stringify([city, scopedName])}
      city={city}
      scopedName={scopedName}
    />
  );
}

function OrderDetailContent({ city, scopedName }: { city: string | null; scopedName: string }) {
  const now = useNow();
  const [selectedOutput, setSelectedOutput] = useState<{
    city: string | null;
    scopedName: string;
    entry: SupervisorOrderHistoryEntry;
  } | null>(null);

  const orderSource = useCachedData(`orders:detail:${city ?? 'no-city'}:${scopedName}`, () =>
    getSupervisorOrder(scopedName),
  );
  const historySource = useCachedData(`orders:history:${city ?? 'no-city'}:${scopedName}`, () =>
    listSupervisorOrderHistory(scopedName),
  );

  const refresh = useCallback(async () => {
    await Promise.all([orderSource.refresh(), historySource.refresh()]);
  }, [orderSource, historySource]);
  useVisibleRefresh(refresh, 30_000);

  const order = orderSource.data ?? null;
  const history = historySource.data ?? [];
  const historyReady =
    historySource.data !== undefined && !historySource.loading && historySource.error === null;
  const visibleOutput =
    historyReady &&
    selectedOutput !== null &&
    selectedOutput.city === city &&
    selectedOutput.scopedName === scopedName &&
    history.some(
      (entry) =>
        entry.store_ref === selectedOutput.entry.store_ref &&
        entry.bead_id === selectedOutput.entry.bead_id,
    )
      ? selectedOutput.entry
      : null;
  const loading = orderSource.loading || historySource.loading;
  const error =
    [orderSource.error, historySource.error]
      .filter((value): value is string => value !== null)
      .join('; ') || null;

  return (
    <section>
      <PageHeader
        title={order?.name ?? scopedName}
        synopsis={
          order?.description ?? (order === null && error === null ? 'Loading order.' : null)
        }
        meta={
          <>
            {error && (
              <span className="normal-case text-body text-accent" role="alert">
                {error}
              </span>
            )}
            <Link to="/orders">
              <Button size="sm" tone="quiet">
                ← Orders
              </Button>
            </Link>
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

      {order === null ? (
        error === null && <p className="text-body text-fg-muted italic">Loading order.</p>
      ) : (
        <div className="space-y-10">
          <dl className="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field label="Status">
              {order.enabled ? (
                <StatusBadge tone="ok" label="enabled" />
              ) : (
                <StatusBadge tone="neutral" label="disabled" />
              )}
            </Field>
            <Field label="Scope">{order.rig ?? 'city'}</Field>
            <Field label="Runs">{order.formula ?? order.exec ?? '—'}</Field>
            <Field label="Trigger">{order.trigger ?? '—'}</Field>
            <Field label="On">{order.on ?? '—'}</Field>
            <Field label="Interval">{order.interval ?? '—'}</Field>
            <Field label="Schedule">{order.schedule ?? '—'}</Field>
            <Field label="Check">{order.check ?? '—'}</Field>
            <Field label="Pool">{order.pool ?? '—'}</Field>
          </dl>

          <section>
            <h2 className="text-label uppercase tracking-wider text-fg-muted mb-3">Recent runs</h2>
            {historySource.error !== null ? (
              <p className="text-body text-fg-muted italic">History unavailable.</p>
            ) : historyReady ? (
              <Table
                columns={historyColumns(now, visibleOutput, (entry) =>
                  setSelectedOutput(entry === null ? null : { city, scopedName, entry }),
                )}
                rows={history}
                rowKey={historyRowKey}
                empty="No recorded runs."
              />
            ) : (
              <p className="text-body text-fg-muted italic">Loading history.</p>
            )}
            {visibleOutput !== null && (
              <OrderOutputViewer
                key={JSON.stringify([
                  city,
                  scopedName,
                  visibleOutput.store_ref,
                  visibleOutput.bead_id,
                ])}
                city={city}
                scopedName={scopedName}
                entry={visibleOutput}
              />
            )}
          </section>
        </div>
      )}
    </section>
  );
}

function historyRowKey(entry: SupervisorOrderHistoryEntry): string {
  return `${entry.store_ref}:${entry.bead_id}`;
}

function historyColumns(
  now: number,
  selectedOutput: SupervisorOrderHistoryEntry | null,
  setSelectedOutput: (entry: SupervisorOrderHistoryEntry | null) => void,
): ReadonlyArray<TableColumn<SupervisorOrderHistoryEntry>> {
  return [
    {
      key: 'time',
      label: 'Time',
      render: (entry) => (
        <span title={entry.created_at}>{formatRelative(entry.created_at, now)} ago</span>
      ),
    },
    {
      key: 'duration',
      label: 'Duration',
      render: (entry) => <span>{formatDurationMs(entry.duration_ms)}</span>,
    },
    {
      key: 'exit',
      label: 'Exit',
      render: (entry) => <span>{entry.exit_code ?? '—'}</span>,
    },
    {
      key: 'outcome',
      label: 'Outcome',
      render: (entry) =>
        entry.outcome !== undefined && entry.outcome !== '' ? (
          <StatusBadge
            tone={
              entry.outcome === 'success' ? 'ok' : entry.outcome === 'failed' ? 'stuck' : 'neutral'
            }
            label={entry.outcome}
          />
        ) : (
          <span className="text-fg-muted">—</span>
        ),
    },
    {
      key: 'bead',
      label: 'Bead',
      render: (entry) => <span className="text-fg-muted">{entry.bead_id}</span>,
    },
    {
      key: 'output',
      label: 'Output',
      render: (entry) =>
        entry.has_output ? (
          <Button
            size="sm"
            tone="quiet"
            onClick={() =>
              setSelectedOutput(
                selectedOutput?.bead_id === entry.bead_id &&
                  selectedOutput.store_ref === entry.store_ref
                  ? null
                  : entry,
              )
            }
          >
            {selectedOutput?.bead_id === entry.bead_id &&
            selectedOutput.store_ref === entry.store_ref
              ? 'Hide output'
              : 'View output'}
          </Button>
        ) : (
          <span className="text-fg-muted">—</span>
        ),
    },
  ];
}

function OrderOutputViewer({
  city,
  scopedName,
  entry,
}: {
  city: string | null;
  scopedName: string;
  entry: SupervisorOrderHistoryEntry;
}) {
  const { data, loading, error } = useCachedData(
    `orders:output:${JSON.stringify([city, scopedName, entry.store_ref, entry.bead_id])}`,
    () => getSupervisorOrderHistoryDetail(entry.bead_id, entry.store_ref),
  );
  return (
    <section aria-label={`Output for ${entry.bead_id}`} className="mt-5 space-y-2">
      <h3 className="text-label uppercase tracking-wider text-fg-muted">
        Run output · {entry.bead_id}
      </h3>
      {loading && <p className="text-body text-fg-muted">Loading output.</p>}
      {error !== null && (
        <p className="text-body text-accent" role="alert">
          {error}
        </p>
      )}
      {data !== undefined && (
        <pre className="text-body whitespace-pre-wrap break-words rounded-sm bg-surface-tint p-4">
          {data.output || 'No stored output returned.'}
        </pre>
      )}
    </section>
  );
}

function formatDurationMs(raw: string | undefined): string {
  if (raw === undefined) return '—';
  const ms = Number(raw);
  if (!Number.isFinite(ms)) return '—';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}
