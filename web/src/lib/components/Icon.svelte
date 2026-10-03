<script lang="ts">
  let {
    name,
    size = 20,
  }: {
    name:
      | "Send"
      | "Receive"
      | "History"
      | "Settings"
      | "Account"
      | "Devices"
      | "Users"
      | "Upload"
      | "Lock"
      | "Arrow";
    size?: number;
  } = $props();
  const paths = {
    Send: "m22 2-7 20-4-9-9-4 20-7ZM22 2 11 13",
    Receive: "M12 3v12m-5-5 5 5 5-5M4 15v5a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5",
    History: "M3 11a9 9 0 1 1 2.6 7M3 4v7h7m2-4v5l3 2",
    Settings: "M4 6h16M4 12h16M4 18h16M8 3v6m8 0v6m-8 0v6",
    Account: "M20 21v-2a6 6 0 0 0-6-6h-4a6 6 0 0 0-6 6v2M16 6a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
    Devices: "M7 2h10a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Zm3 17h4",
    Users:
      "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.87M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Zm4-3.87a4 4 0 0 1 0 7.75",
    Upload: "M12 16V3m-5 5 5-5 5 5M4 16v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4",
    Lock: "M5 10h14v11H5V10Zm3 0V6a4 4 0 0 1 8 0v4m-4 4v3",
    Arrow: "M5 12h14m-5-5 5 5-5 5",
  };
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.7"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"><path d={paths[name]} /></svg
>
