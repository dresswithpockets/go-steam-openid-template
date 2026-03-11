<script lang="ts">
  import { browser } from '$app/environment';
  import { Client } from '$lib/api';
  import { ApiPaths } from '$lib/schema';
  import { onMount } from 'svelte';

  async function handleSignin() {
    const { response } = await Client.GET(ApiPaths.get_internal_session_steam_discover);
    const location = response.headers.get("Location");
    console.log("response: ", response, "location: ", location, "browser: ", browser);
    if (location && browser) {
        console.log("ready");
        window.location.assign(location);
    }
  }

  onMount(async () => { await handleSignin(); });
</script>
bweep