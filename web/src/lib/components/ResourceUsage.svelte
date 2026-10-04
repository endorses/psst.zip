<script lang="ts">
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

<div class="usage" aria-label={`${scope === "server" ? "Server" : "Account"} resource usage`}>
  <dl>
    <div>
      <dt>Tracked occupied storage (estimate)</dt>
      <dd>{capacityLabel(snapshot.usage.occupied_bytes)}</dd>
    </div>
    <div>
      <dt>Reserved storage, including occupied</dt>
      <dd>{capacityLabel(snapshot.usage.reserved_bytes)}</dd>
    </div>
    <div>
      <dt>Storage quota</dt>
      <dd>{capacityLabel(quota.storage)}</dd>
    </div>
    <div>
      <dt>Available within storage quota</dt>
      <dd>{capacityLabel(remainingCapacity(quota.storage, snapshot.usage.reserved_bytes))}</dd>
    </div>
  </dl>
  <div class="table-scroll">
    <table>
      <caption>Current object allocations</caption>
      <thead
        ><tr
          ><th scope="col">Resource</th><th scope="col">Used</th><th scope="col">Quota</th><th
            scope="col">Available</th
          ></tr
        ></thead
      >
      <tbody
        >{#each [{ key: "files" as const, label: "Files" }, { key: "transfers" as const, label: "Transfers" }, { key: "slots" as const, label: "Receive links" }] as row}<tr
          >
            <th scope="row">{row.label}</th><td>{snapshot.usage[row.key]}</td><td
              >{quota[row.key]}</td
            ><td>{remainingCapacity(quota[row.key], snapshot.usage[row.key])}</td>
          </tr>{/each}</tbody
      >
    </table>
  </div>
  {#if snapshot.usage.reserved_bytes > quota.storage || snapshot.usage.files > quota.files || snapshot.usage.transfers > quota.transfers || snapshot.usage.slots > quota.slots}<p
      class="notice"
    >
      Current allocations exceed a quota. Existing data remains available; new allocations may be
      blocked until capacity is released or the administrator raises the quota.
    </p>{/if}
  <p class="muted small">
    Occupied storage follows persisted upload progress and can lag physical writes after a crash.
    Reservations include encrypted files, manifests and unfinished uploads. Retained metadata counts
    toward object quotas. Capacity is released after verified deletion; cumulative limits on receive
    links do not refill. Available quota is not a disk-space guarantee: server-wide limits and the
    free-disk reserve also apply.
  </p>
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
