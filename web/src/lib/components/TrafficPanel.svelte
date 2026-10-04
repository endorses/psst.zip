<script lang="ts">
  import { onMount } from "svelte";
  import TrafficBudgetSettings from "./TrafficBudgetSettings.svelte";
  import { accountRequest } from "$lib/account";
  import { formatSize } from "$lib/upload-job.svelte";
  import { utcTime, measurementExplanation, type TrafficReport } from "$lib/admin";
  let report = $state<TrafficReport | null>(null),
    busy = $state(false),
    error = $state(""),
    notice = $state("");
  let from = $state(""),
    to = $state(""),
    allowance = $state<number | undefined>(),
    day = $state(1),
    basis = $state<"outbound" | "combined">("outbound");
  let disposed = false;
  const peak = $derived(
    Math.max(1, ...(report?.days.map((d) => Math.max(d.uploaded_bytes, d.downloaded_bytes)) ?? [])),
  );
  async function load() {
    busy = true;
    error = "";
    try {
      const next = await accountRequest<TrafficReport>(
        `/admin/traffic${from && to ? `?from=${from}&to=${to}` : ""}`,
      );
      if (disposed) return;
      report = next;
      from = next.range.from;
      to = next.range.to;
      allowance =
        next.settings.allowance_bytes === null
          ? undefined
          : next.settings.allowance_bytes / 1024 ** 3;
      day = next.settings.cycle_start_day;
      basis = next.settings.basis;
    } catch (e) {
      if (!disposed) error = e instanceof Error ? e.message : "Traffic is unavailable.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = "";
    notice = "";
    try {
      await accountRequest("/admin/traffic/settings", "PATCH", {
        allowance_bytes: allowance === undefined ? null : Math.round(allowance * 1024 ** 3),
        cycle_start_day: day,
        basis,
      });
      await load();
      notice = "Traffic allowance saved. It monitors usage and does not block transfers.";
    } catch (e) {
      error = e instanceof Error ? e.message : "Could not save settings.";
    } finally {
      busy = false;
    }
  }
  onMount(() => {
    void load();
    return () => {
      disposed = true;
    };
  });
</script>

<h1>Traffic</h1>
<p class="muted">Application transfer traffic · UTC</p>
<TrafficBudgetSettings />
{#if error}<p class="error" role="alert">
    {error}
    {report ? "The previous measurements remain below and may be stale." : ""}
  </p>{/if}
{#if notice}<p role="status" class="notice">{notice}</p>{/if}
{#if report}
  {#if report.status !== "ok"}<p class="error" role="alert">
      Traffic accounting is degraded. These measurements may be incomplete.
    </p>{/if}
  <p class="muted small">
    Measured since {utcTime(report.recording_started_at)} · Updated {utcTime(report.updated_at)}.
    Earlier activity is not included; the first billing period may be partial.
  </p>
  <div class="metrics">
    <article>
      <span>Today</span><strong>{formatSize(report.today.total_bytes)}</strong><small
        >Combined traffic</small
      >
    </article>
    <article>
      <span>This calendar month</span><strong>{formatSize(report.month.total_bytes)}</strong><small
        >Combined traffic</small
      >
    </article>
    <article>
      <span>Measured lifetime</span><strong>{formatSize(report.lifetime.total_bytes)}</strong><small
        >Combined traffic</small
      >
    </article>
  </div>
  <h2>Current billing cycle</h2>
  <p>{report.cycle.start} to {report.cycle.end} (end exclusive, UTC)</p>
  <p>
    <strong>{formatSize(report.cycle.counted_bytes)}</strong> used · {report.settings.basis ===
    "outbound"
      ? "Outbound only"
      : "Upload and download combined"}
  </p>
  {#if report.settings.allowance_bytes !== null}<progress
      aria-label="Traffic allowance used"
      max={report.settings.allowance_bytes || 1}
      value={Math.min(report.cycle.counted_bytes, report.settings.allowance_bytes || 1)}
    ></progress>
    <p>
      {formatSize(Math.max(0, report.cycle.remaining_bytes ?? 0))} remaining of {formatSize(
        report.settings.allowance_bytes,
      )}{report.cycle.counted_bytes > report.settings.allowance_bytes
        ? " · Allowance exceeded"
        : ""}
    </p>{:else}<p class="muted">No allowance configured.</p>{/if}
{/if}
<form
  class="range"
  onsubmit={(e) => {
    e.preventDefault();
    void load();
  }}
>
  <label>From (UTC)<input type="date" required bind:value={from} /></label><label
    >Through (UTC, inclusive)<input type="date" required min={from} bind:value={to} /></label
  ><button disabled={busy}>{busy ? "Loading…" : "Show traffic"}</button>
</form>
{#if report}
  <p class="muted small">
    Choose up to 367 days. Measured lifetime totals above include the full recorded history.
  </p>
  <h2>Selected period</h2>
  <p>
    Uploaded {formatSize(report.totals.uploaded_bytes)} · Downloaded {formatSize(
      report.totals.downloaded_bytes,
    )} · Combined {formatSize(report.totals.total_bytes)}
  </p>
  <div
    class="chart"
    style:gap={report.days.length <= 31 ? "3px" : "0"}
    role="img"
    aria-label="Daily uploaded and downloaded traffic; exact byte values are in the table below."
  >
    {#each report.days as point}<div
        class="bar-pair"
        title={`${point.date}: uploaded ${point.uploaded_bytes} bytes; downloaded ${point.downloaded_bytes} bytes`}
      >
        <span class="upload" style:height={`${(100 * point.uploaded_bytes) / peak}%`}></span><span
          class="download"
          style:height={`${(100 * point.downloaded_bytes) / peak}%`}
        ></span>
      </div>{/each}
  </div>
  <p class="legend"><span>● Uploaded</span><span>▰ Downloaded</span></p>
  <details>
    <summary>Daily traffic data (exact bytes)</summary>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard focus lets arrow keys scroll all data columns.) -->
    <div class="table-scroll" role="region" aria-label="Daily traffic data" tabindex="0">
      <table>
        <caption>Daily application traffic in UTC</caption><thead
          ><tr
            ><th>Date</th><th>Uploaded bytes</th><th>Downloaded bytes</th><th>Combined bytes</th
            ></tr
          ></thead
        ><tbody
          >{#each report.days as point}<tr
              ><th>{point.date}</th><td>{point.uploaded_bytes.toLocaleString()}</td><td
                >{point.downloaded_bytes.toLocaleString()}</td
              ><td>{point.total_bytes.toLocaleString()}</td></tr
            >{/each}</tbody
        >
      </table>
    </div>
  </details>
  <h2>Monitoring allowance settings</h2>
  <form onsubmit={save}>
    <label
      >Allowance (GiB, optional)<input
        type="number"
        min="0.001"
        step="any"
        bind:value={allowance}
      /></label
    ><label
      >Cycle starts on day (UTC)<input
        type="number"
        min="1"
        max="31"
        step="1"
        required
        bind:value={day}
      /></label
    >
    <p class="muted small">
      If this day is missing in a shorter month, the cycle starts on its last day.
    </p>
    <label
      >Count toward allowance<select bind:value={basis}
        ><option value="outbound">Outbound downloads only</option><option value="combined"
          >Uploads and downloads</option
        ></select
      ></label
    ><button class="primary" disabled={busy}>Save traffic settings</button>
  </form>
{/if}
<p class="muted small">
  {measurementExplanation} The monitoring allowance does not block transfers; enforced budgets are configured
  separately above.
</p>
{#if !report}<button onclick={load} disabled={busy}
    >{busy ? "Loading traffic…" : "Retry traffic"}</button
  >{/if}

<style>
  .metrics {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: 1rem;
    margin: 1.5rem 0;
  }
  .metrics article {
    background: var(--elevated);
    padding: 1rem;
    border-radius: 12px;
    display: grid;
    gap: 0.4rem;
  }
  .metrics strong {
    font-size: 1.4rem;
  }
  .metrics small {
    color: var(--muted);
  }
  .range {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: 1rem;
  }
  .range label {
    flex: 1;
    min-width: 10rem;
  }
  form {
    max-width: 42rem;
  }
  .chart {
    height: 150px;
    display: flex;
    align-items: end;
    border-bottom: 1px solid var(--divider);
    margin-top: 2rem;
  }
  .bar-pair {
    display: flex;
    align-items: end;
    gap: 0;
    flex: 1 1 0%;
    min-width: 0;
    height: 100%;
  }
  .bar-pair span {
    flex: 1 1 0%;
    min-width: 0;
    min-height: 1px;
  }
  .upload {
    background: var(--primary);
  }
  .download {
    background: var(--muted);
    border-top: 2px solid var(--text);
  }
  .legend {
    display: flex;
    gap: 1rem;
    font-size: 0.8rem;
  }
  .legend span:first-child {
    color: var(--primary);
  }
  .table-scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  td,
  th {
    padding: 0.5rem;
  }
  th:first-child {
    text-align: left;
  }
  summary {
    min-height: 44px;
    cursor: pointer;
  }
  progress {
    width: 100%;
  }
</style>
