<script lang="ts">
  import a from "./a.json";
  // @ts-ignore
  import b from "./grpc.svg";
  import { type Hello, HelloSchema } from "@grpcview/v1/service_pb";

  console.log(HelloSchema.fields);
  const m: Hello = { $typeName: "grpcview.v1.Hello", a: "" };

  console.log(m);

  console.log(a.asdf);
  console.log(b);
</script>

<main>567s47</main>
