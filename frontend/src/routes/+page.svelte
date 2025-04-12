<script>
  let count = $state(3);

  const inc = () => {
    count++;
  };
</script>

<button onclick={inc}>Welcome to {count} </button>
<p>
  Visit <a href="https://svelte.dev/docs/kit">svelte.dev/docs/kit</a> to read the
  documentation
</p>
