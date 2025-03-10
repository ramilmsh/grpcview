package wasmprotoc

import (
	"fmt"

	"github.com/bytecodealliance/wasmtime-go/v30"

	_ "embed"
)

//go:embed protoc-gen-openapiv2.wasm
var protocGenOpenapiV2Wasm []byte

func GenOpenapiV2(data []byte) error {
	engine := wasmtime.NewEngine()
	linker := wasmtime.NewLinker(engine)
	if err := linker.DefineWasi(); err != nil {
		return err
	}
	cfg := wasmtime.NewWasiConfig()
	cfg.SetArgv([]string{
		"",
		"-allow_delete_body=true",
		"-output_format=yaml",
		"-allow_merge=true",
		"-merge_file_name=openapiv2",
	})
	cfg.SetStdinBytes(data)
	cfg.InheritStdout()
	cfg.InheritStderr()

	store := wasmtime.NewStore(engine)
	store.SetWasi(cfg)

	module, err := wasmtime.NewModule(engine, protocGenOpenapiV2Wasm)
	if err != nil {
		return err
	}

	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return err
	}

	_, err = instance.GetFunc(store, "_start").Call(store)

	fmt.Println(err)

	return nil
}
