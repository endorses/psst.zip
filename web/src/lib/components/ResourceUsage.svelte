<script lang="ts">
  import { message as m, t, date } from "$lib/i18n";

  import { capacityLabel, remainingCapacity, type ResourceSnapshot } from "$lib/resource-policy";
  let {
    snapshot,
    scope = "account",
  }: { snapshot: ResourceSnapshot; scope?: "server" | "account" } = $props();
  const quota = $derived({
    storage: snapshot.policy[`${scope}_storage_bytes`],
    files: snapshot.policy[`${scope}_files`],
    transfers: snapshot.policy[`${scope}_transfers`],
    slots: snapshot.policy[`${scope}_slots`],
  });
</script>

<div
  class="usage"
  aria-label={$t(
    m("valueResourceUsage", { arg0: scope === "server" ? m("server") : m("account") }),
  )}
>
  <dl>
    <div>
      <dt>{$t(m("trackedOccupiedStorageEstimate"))}</dt>
      <dd>{$t(capacityLabel(snapshot.usage.occupied_bytes))}</dd>
    </div>
    <div>
      <dt>{$t(m("reservedStorageIncludingOccupied"))}</dt>
      <dd>{$t(capacityLabel(snapshot.usage.reserved_bytes))}</dd>
    </div>
    <div>
      <dt>{$t(m("storageQuota"))}</dt>
      <dd>{$t(capacityLabel(quota.storage))}</dd>
    </div>
    <div>
      <dt>{$t(m("availableWithinStorageQuota"))}</dt>
      <dd>{$t(capacityLabel(remainingCapacity(quota.storage, snapshot.usage.reserved_bytes)))}</dd>
    </div>
  </dl>
  {#if snapshot.capacity}
    <div class="capacity-snapshot" aria-label={$t(m("currentUploadCapacity"))}>
      {#if snapshot.capacity.state === "unknown"}
        <p class="notice">{$t(m("currentDiskCapacityCouldNotBeCheckedTheQuota"))}</p>
      {:else if snapshot.capacity.state === "blocked"}
        <p class="notice">
          {$t(
            snapshot.capacity.reason === "disk_capacity"
              ? m("newFileAllocationsAreBlockedByTheDiskSafety")
              : m("newFileAllocationsHaveReachedAServerOrAccount"),
          )}
          {$t(m("existingLocalFilesAndAdministrativeRecoveryRemainAvailable"))}
        </p>
      {:else}
        <p>
          <strong
            >{$t(capacityLabel(snapshot.capacity.available_wire_bytes!))}
            {$t(m("currentlyAvailableForNewEncryptedFileAllocations"))}</strong
          >
        </p>
      {/if}
      <p class="muted small">
        {$t(m("checked"))}
        {$t(date(snapshot.capacity.checked_at))}{$t(
          m("thisIncludesServerWideLimitsAndTheDiskReserve"),
        )}
      </p>
    </div>
  {/if}
  <div class="table-scroll">
    <table>
      <caption>{$t(m("currentObjectAllocations"))}</caption>
      <thead
        ><tr
          ><th scope="col">{$t(m("resource"))}</th><th scope="col">{$t(m("used"))}</th><th
            scope="col">{$t(m("quota"))}</th
          ><th scope="col">{$t(m("available"))}</th></tr
        ></thead
      >
      <tbody
        >{#each [{ key: "files" as const, label: m("files") }, { key: "transfers" as const, label: m("transfers") }, { key: "slots" as const, label: m("receiveLinks") }] as row}<tr
          >
            <th scope="row">{$t(row.label)}</th><td>{$t(snapshot.usage[row.key])}</td><td
              >{$t(quota[row.key])}</td
            ><td>{$t(remainingCapacity(quota[row.key], snapshot.usage[row.key]))}</td>
          </tr>{/each}</tbody
      >
    </table>
  </div>
  {#if snapshot.usage.reserved_bytes > quota.storage || snapshot.usage.files > quota.files || snapshot.usage.transfers > quota.transfers || snapshot.usage.slots > quota.slots}<p
      class="notice"
    >
      {$t(m("currentAllocationsExceedAQuotaExistingDataRemainsAvailable"))}
    </p>{/if}
  <p class="muted small">{$t(m("occupiedStorageFollowsPersistedUploadProgressAndCanLag"))}</p>
</div>

<style>
  dl {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
    gap: 1rem;
  }
  dd {
    margin: 0.25rem 0 0;
    font-weight: 650;
  }
  dt {
    color: var(--muted);
    font-size: 0.85rem;
  }
  .table-scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    padding: 0.6rem 0.35rem;
    text-align: right;
    border-bottom: 1px solid var(--divider);
  }
  th:first-child {
    text-align: left;
  }
  caption {
    text-align: left;
    margin: 1rem 0 0.4rem;
    font-weight: 600;
  }
</style>
