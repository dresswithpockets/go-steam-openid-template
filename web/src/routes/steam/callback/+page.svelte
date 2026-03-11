<script lang="ts">
  import { page } from '$app/state';
  import { Client } from '$lib/api';
  import { ApiPaths } from '$lib/schema';

  async function verifyCallback() {
    const queryParams = page.url.searchParams;
    console.log('TO STRING');
    console.log(page.url.toString());
    console.log(page.url.searchParams);

    const { data } = await Client.GET(ApiPaths.get_internal_session_steam_callback, {
      params: { query: Object.fromEntries(queryParams) }
    });
    console.log(data);
  }

  verifyCallback();
</script>