package zkwasm_wasmi

/*
#cgo CFLAGS: -I${SRCDIR}/packaged/include
#cgo LDFLAGS: -lwasmi_c_api -pthread

#cgo linux,amd64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/linux-amd64 -L${SRCDIR}/packaged/lib/linux-amd64
//#cgo linux,arm64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/linux-aarch64 -L${SRCDIR}/packaged/lib/linux-aarch64
#cgo darwin,amd64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/darwin-amd64 -L${SRCDIR}/packaged/lib/darwin-amd64
#cgo darwin,arm64 LDFLAGS: -Wl,-rpath,${SRCDIR}/packaged/lib/darwin-aarch64 -L${SRCDIR}/packaged/lib/darwin-aarch64

#include <stdint.h>
#include <stdlib.h>

typedef void (*callback_fn_t)(int32_t engine_id, char* fn_name, int32_t* data, int32_t data_len);

void callbackHandle_cgo(int32_t engine_id, char* fn_name, int32_t* data, int32_t data_len);

#include "packaged/include/wasmi.h"
*/
import "C"

import (
    _ "embed"
    "log"
    "reflect"
    "sync"
    "unsafe"

    _ "github.com/wasm0/zkwasm-wasmi/packaged/include"
    _ "github.com/wasm0/zkwasm-wasmi/packaged/lib"
)

func byteArrayToRawPointer(input []byte) (*C.uchar, C.size_t) {
	var argv = make([]C.uchar, len(input))
	for i, item := range input {
		argv[i] = C.uchar(item)
	}
	return (*C.uchar)(unsafe.Pointer(&argv[0])), C.size_t(len(input))
}

func ExecuteWasmBinaryToJson(wasmBinary []byte) (traceJson []byte, err error) {
	cVec, cLen := byteArrayToRawPointer(wasmBinary)
	res := C.execute_wasm_binary_to_json(cVec, cLen)
	traceJson = C.GoBytes(unsafe.Pointer(res.ptr), C.int(res.len))
	return traceJson, nil
}

type WasmEnginesPool struct {
	// engine_id -> WasmEngine
	pool     map[int32]*WasmEngine
	poolLock sync.Mutex
}

func NewWasmEnginesPool() *WasmEnginesPool {
	return &WasmEnginesPool{
		pool: make(map[int32]*WasmEngine),
	}
}

func (wep *WasmEnginesPool) Add(id int32, engine *WasmEngine) bool {
	wep.poolLock.Lock()
	defer wep.poolLock.Unlock()

	if _, ok := wep.pool[id]; ok {
		return false
	}
	wep.pool[id] = engine

	return true
}

func (wep *WasmEnginesPool) Get(id int32) *WasmEngine {
	wep.poolLock.Lock()
	defer wep.poolLock.Unlock()

	if we, ok := wep.pool[id]; ok {
		return we
	}
	return nil
}

var wasmEnginesPool = NewWasmEnginesPool()

type WasmEngine struct {
	id             int32
	execContexts   map[string]ExecContext
	registeredLock sync.Mutex
}

type ExecContext interface {
	//Callback interface{}
	//Context  interface{}
}

func createWasmEngine() (id int32, err error) {
	engine_id := C.create_wasm_engine()
	engineId := int32(engine_id)
	return engineId, nil
}

func NewWasmEngine() *WasmEngine {
	id, _ := createWasmEngine()
	entity := &WasmEngine{
		id:           id,
		execContexts: make(map[string]ExecContext),
	}
	ok := wasmEnginesPool.Add(id, entity)
	if !ok {
		log.Panicf("tried to register wasm engine with existing id %d\n", id)
	}
	return entity
}

func (we *WasmEngine) SetWasmBinary(wasmBinary []byte) bool {
	cVec, cLen := byteArrayToRawPointer(wasmBinary)
	res := C.set_wasm_binary(C.int(we.id), cVec, cLen)
	return bool(res)
}

func (we *WasmEngine) ComputeTrace() (traceJson []byte, err error) {
	res := C.compute_trace(C.int(we.id))
	traceJson = C.GoBytes(unsafe.Pointer(res.ptr), C.int(res.len))
	return traceJson, nil
}

func (we *WasmEngine) MemoryData() (data []byte, err error) {
	res := C.memory_data(C.int(we.id))
	data = C.GoBytes(unsafe.Pointer(res.ptr), C.int(res.len))
	return data, nil
}

func (we *WasmEngine) TraceMemoryChange(offset, len uint32, data []byte) (err error) {
	cVec, cLen := byteArrayToRawPointer(data)
	C.trace_memory_change(C.int(we.id), C.uint32_t(offset), C.uint32_t(len), cVec, cLen)
	return nil
}

func (we *WasmEngine) register(name string, execContext ExecContext) {
	we.registeredLock.Lock()
	defer we.registeredLock.Unlock()

	if _, ok := we.execContexts[name]; ok {
		log.Panicf("name '%s' already occupied\n", name)
	}
	we.execContexts[name] = execContext
}

func (we *WasmEngine) getRegistered(name string) ExecContext {
	we.registeredLock.Lock()
	defer we.registeredLock.Unlock()

	found, ok := we.execContexts[name]
	if !ok {
		log.Panicf("nothing registered for name '%s'\n", name)
	}
	return found
}

func (we *WasmEngine) RegisterHostFn2(fnName string, paramsCount int, fn ExecContext) bool {
	we.register(fnName, fn)
	funcNameCStr := C.CString(fnName)
	defer C.free(unsafe.Pointer(funcNameCStr))
	result := false
	res := C.register_host_fn(C.int(we.id), (*C.int8_t)(funcNameCStr), (C.callback_fn_t)(C.callbackHandle_cgo), C.int32_t(paramsCount))
	result = bool(res)
	return result
}

func cArrayToSlice(array *C.int32_t, len C.int) []int32 {
	var list []int32
	sliceHeader := (*reflect.SliceHeader)(unsafe.Pointer(&list))
	sliceHeader.Cap = int(len)
	sliceHeader.Len = int(len)
	sliceHeader.Data = uintptr(unsafe.Pointer(array))
	return list
}

func cArrayToString(array *C.char, len C.int) string {
	var list []byte
	sliceHeader := (*reflect.SliceHeader)(unsafe.Pointer(&list))
	sliceHeader.Cap = int(len)
	sliceHeader.Len = int(len)
	sliceHeader.Data = uintptr(unsafe.Pointer(array))
	return string(list)
}

func cCharPtrToString(p *C.char) string {
	s := C.GoString(p)
	C.free(unsafe.Pointer(p))
	return s
}

//export callbackHandle_cgo
func callbackHandle_cgo(engine_id C.int32_t, fn_name *C.char, data *C.int32_t, data_len C.int32_t) {
	//const FN_NAME = "_evm_return"
	engineId := int32(engine_id)
	fnName := cCharPtrToString(fn_name)
	args := cArrayToSlice(data, data_len)
	wasmEngine := wasmEnginesPool.Get(engineId)
	if wasmEngine == nil {
		log.Panicf("wasm engine id %d doesn't exist", engineId)
	}
	execContext := wasmEngine.getRegistered(fnName)
	if cb, ok := execContext.(func(params []int32)); ok {
		cb(args[1:])
	} else {
		log.Panicf("failed to cast fnName '%s', check registered function\n", fnName)
	}
}