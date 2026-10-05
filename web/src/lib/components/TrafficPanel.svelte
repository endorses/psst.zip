<script lang="ts">
  import {
    message as m,
    t,
    calendarDate,
    date,
    number,
    errorText,
    translate,
    type DisplayText,
  } from "$lib/i18n";

  import { onMount } from "svelte";
  import TrafficBudgetSettings from "./TrafficBudgetSettings.svelte";
  import { accountRequest, AccountError } from "$lib/account";
  import { formatSize } from "$lib/upload-job.svelte";
  import { utcTime, measurementExplanation, type TrafficReport } from "$lib/admin";
  let report = $state<TrafficReport | null>(null),
    busy = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
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
      if (!disposed)
        error =
          e instanceof AccountError && e.code === "traffic_history_unavailable"
            ? m("thisPeriodIsOutsideTheAvailableDailyHistoryChoose")
            : e instanceof Error
              ? errorText(e)
              : m("trafficIsUnavailable");
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
      notice = m("trafficAllowanceSavedItMonitorsUsageAndDoesNot");
    } catch (e) {
      error = e instanceof Error ? errorText(e) : m("couldNotSaveSettings");
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

<h1>{$t(m("traffic"))}</h1>
<p class="muted">{$t(m("applicationTransferTrafficUTC"))}</p>
<TrafficBudgetSettings />
{#if error}<p class="error" role="alert">
    {$t(error)}
    {$t(report ? m("thePreviousMeasurementsRemainBelowAndMayBeStale") : "")}
  </p>{/if}
{#if notice}<p role="status" class="notice">{$t(notice)}</p>{/if}
{#if report}
  {#if report.status !== "ok"}<p class="error" role="alert">
      {$t(m("trafficAccountingIsDegradedTheseMeasurementsMayBeIncomplete"))}
    </p>{/if}
  <p class="muted small">
    {$t(m("measuredSince"))}
    {$t(utcTime(report.recording_started_at))}
    {$t(m("updated"))}
    {$t(utcTime(report.updated_at))}{$t(m("earlierActivityIsNotIncludedTheFirstBillingPeriod"))}
  </p>
  <div class="metrics">
    <article>
      <span>{$t(m("today"))}</span><strong>{$t(formatSize(report.today.total_bytes))}</strong><small
        >{$t(m("combinedTraffic"))}</small
      >
    </article>
    <article>
      <span>{$t(m("thisCalendarMonth"))}</span><strong
        >{$t(formatSize(report.month.total_bytes))}</strong
      ><small>{$t(m("combinedTraffic"))}</small>
    </article>
    <article>
      <span>{$t(m("measuredLifetime"))}</span><strong
        >{$t(formatSize(report.lifetime.total_bytes))}</strong
      ><small>{$t(m("combinedTraffic"))}</small>
    </article>
  </div>
  <h2>{$t(m("currentBillingCycle"))}</h2>
  <p>
    {$t(utcTime(report.cycle.start))}
    {$t(m("to"))}
    {$t(utcTime(report.cycle.end))}
    {$t(m("endExclusiveUTC"))}
  </p>
  <p>
    <strong>{$t(formatSize(report.cycle.counted_bytes))}</strong>
    {$t(m("used_9e43a"))}
    {$t(report.settings.basis === "outbound" ? m("outboundOnly") : m("uploadAndDownloadCombined"))}
  </p>
  {#if report.settings.allowance_bytes !== null}<progress
      aria-label={$t(m("trafficAllowanceUsed"))}
      max={report.settings.allowance_bytes || 1}
      value={Math.min(report.cycle.counted_bytes, report.settings.allowance_bytes || 1)}
    ></progress>
    <p>
      {$t(formatSize(Math.max(0, report.cycle.remaining_bytes ?? 0)))}
      {$t(m("remainingOf"))}
      {$t(formatSize(report.settings.allowance_bytes))}{$t(
        report.cycle.counted_bytes > report.settings.allowance_bytes ? m("allowanceExceeded") : "",
      )}
    </p>{:else}<p class="muted">{$t(m("noAllowanceConfigured"))}</p>{/if}
{/if}
<form
  class="range"
  onsubmit={(e) => {
    e.preventDefault();
    void load();
  }}
>
  <label
    >{$t(m("fromUTC"))}<input
      type="date"
      required
      min={report?.history_retained_from}
      bind:value={from}
    /></label
  ><label
    >{$t(m("throughUTCInclusive"))}<input
      type="date"
      required
      min={from && from > (report?.history_retained_from ?? "")
        ? from
        : report?.history_retained_from}
      bind:value={to}
    /></label
  ><button disabled={busy}>{$t(busy ? m("loading") : m("showTraffic"))}</button>
</form>
{#if report}
  <p class="muted small">
    {$t(m("dailyDetailsAreRetainedFor"))}
    {$t(report.history_retention_days)}
    {$t(m("daysFrom"))}
    {$t(calendarDate(report.history_retained_from))}
    {$t(m("utcChooseUpToDaysAtATimeMeasured"))}
  </p>
  <h2>{$t(m("selectedPeriod"))}</h2>
  <p class="muted">
    {$t(calendarDate(report.range.from))}
    {$t(m("through"))}
    {$t(calendarDate(report.range.to))}
    {$t(m("inclusiveUTC"))}
  </p>
  <p>
    {$t(m("uploaded"))}
    {$t(formatSize(report.totals.uploaded_bytes))}
    {$t(m("downloaded"))}
    {$t(formatSize(report.totals.downloaded_bytes))}
    {$t(m("combined"))}
    {$t(formatSize(report.totals.total_bytes))}
  </p>
  <div
    class="chart"
    style:gap={$t(report.days.length <= 31 ? "3px" : "0")}
    role="img"
    aria-label={$t(m("dailyUploadedAndDownloadedTrafficExactByteValuesAre"))}
  >
    {#each report.days as point}<div
        class="bar-pair"
        title={$t(
          translate(
            m("chartDailyBytes", {
              date: calendarDate(point.date),
              uploaded: point.uploaded_bytes,
              downloaded: point.downloaded_bytes,
            }),
          ),
        )}
      >
        <span class="upload" style:height={$t(`${(100 * point.uploaded_bytes) / peak}%`)}
        ></span><span
          class="download"
          style:height={$t(`${(100 * point.downloaded_bytes) / peak}%`)}
        ></span>
      </div>{/each}
  </div>
  <p class="legend">
    <span>{$t(m("uploaded_29b1d"))}</span><span>{$t(m("downloaded_e0bc2"))}</span>
  </p>
  <details>
    <summary>{$t(m("dailyTrafficDataExactBytes"))}</summary>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard focus lets arrow keys scroll all data columns.) -->
    <div class="table-scroll" role="region" aria-label={$t(m("dailyTrafficData"))} tabindex="0">
      <table>
        <caption>{$t(m("dailyApplicationTrafficInUTC"))}</caption><thead
          ><tr
            ><th>{$t(m("date"))}</th><th>{$t(m("uploadedBytes"))}</th><th
              >{$t(m("downloadedBytes"))}</th
            ><th>{$t(m("combinedBytes"))}</th></tr
          ></thead
        ><tbody
          >{#each report.days as point}<tr
              ><th>{$t(calendarDate(point.date))}</th><td>{$t(number(point.uploaded_bytes))}</td><td
                >{$t(number(point.downloaded_bytes))}</td
              ><td>{$t(number(point.total_bytes))}</td></tr
            >{/each}</tbody
        >
      </table>
    </div>
  </details>
  <h2>{$t(m("monitoringAllowanceSettings"))}</h2>
  <form onsubmit={save}>
    <label
      >{$t(m("allowanceGiBOptional"))}<input
        type="number"
        min="0.001"
        step="any"
        bind:value={allowance}
      /></label
    ><label
      >{$t(m("cycleStartsOnDayUTC"))}<input
        type="number"
        min="1"
        max="31"
        step="1"
        required
        bind:value={day}
      /></label
    >
    <p class="muted small">{$t(m("ifThisDayIsMissingInAShorterMonth"))}</p>
    <label
      >{$t(m("countTowardAllowance"))}<select bind:value={basis}
        ><option value="outbound">{$t(m("outboundDownloadsOnly"))}</option><option value="combined"
          >{$t(m("uploadsAndDownloads"))}</option
        ></select
      ></label
    ><button class="primary" disabled={busy}>{$t(m("saveTrafficSettings"))}</button>
  </form>
{/if}
<p class="muted small">
  {$t(measurementExplanation)}
  {$t(m("theMonitoringAllowanceDoesNotBlockTransfersEnforcedBudgets"))}
</p>
{#if !report}<button onclick={load} disabled={busy}
    >{$t(busy ? m("loadingTraffic") : m("retryTraffic"))}</button
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
