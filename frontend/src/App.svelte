<script lang="ts">
  import a from "./a.json";
  // @ts-ignore
  import b from "./grpc.svg";
  // @ts-ignore
  import c from "./a.wasm?raw";

  console.log(a.asdf);
  console.log(b);
  console.log(c);

  WebAssembly.instantiate(c);
</script>

<main>567s47</main>
