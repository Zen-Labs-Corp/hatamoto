//go:build wasip1

// Command hostile is a test plugin whose exports misbehave on purpose.
package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/extism/go-pdk"
)

func main() {}

//go:wasmexport echo
func echo() int32 {
	pdk.Output(pdk.Input())
	return 0
}

//go:wasmexport hostile_loop
func hostileLoop() int32 {
	n := 0
	for {
		n++
	}
}

var hoard [][]byte

//go:wasmexport hostile_alloc
func hostileAlloc() int32 {
	for {
		b := make([]byte, 1<<20)
		for i := range b {
			b[i] = 1
		}
		hoard = append(hoard, b)
	}
}

//go:wasmexport hostile_panic
func hostilePanic() int32 {
	var m map[string]int
	m["x"] = 1
	return 0
}

//go:wasmexport hostile_recurse
func hostileRecurse() int32 { return recurse(0) + 1 }

func recurse(n int) int32 {
	var pad [1024]byte
	pad[n%1024] = byte(n)
	return recurse(n+1) + int32(pad[0])
}

// hostile_http tries the Extism HTTP host function against the URL in the input.
//
//go:wasmexport hostile_http
func hostileHTTP() int32 {
	defer func() {
		if r := recover(); r != nil {
			pdk.OutputString("denied")
		}
	}()
	resp := pdk.NewHTTPRequest(pdk.MethodGet, string(pdk.Input())).Send()
	pdk.OutputString("reached:" + strings.TrimSpace(string(resp.Body())))
	return 0
}

// hostile_fs tries to read a host file.
//
//go:wasmexport hostile_fs
func hostileFS() int32 {
	if _, err := os.ReadFile("/etc/passwd"); err != nil {
		pdk.OutputString("denied")
		return 0
	}
	pdk.OutputString("read")
	return 0
}

// guest_error reports an ordinary error through the PDK (non-zero status).
//
//go:wasmexport guest_error
func guestError() int32 {
	pdk.SetErrorString("boom")
	return 1
}

// misbehave does what the manifest config "mode" says, so one module can
// stand in for several hostile plugins at the real hook exports.
func misbehave() int32 {
	mode, _ := pdk.GetConfig("mode")
	switch mode {
	case "loop":
		return hostileLoop()
	case "alloc":
		return hostileAlloc()
	case "panic":
		return hostilePanic()
	case "bigtags":
		n, _ := pdk.GetConfig("bytes")
		size, _ := strconv.Atoi(n)
		pdk.OutputString(bigTags(size))
	case "garbage":
		pdk.OutputString("{not json")
	case "reject":
		pdk.SetErrorString("hatamoto:reject:no thanks")
		return 1
	case "error":
		pdk.SetErrorString("plugin failure")
		return 1
	}
	return 0
}

// bigTags returns {"tags":[{},{},...]} of about size bytes: tiny JSON that
// decodes into one large Go value per element.
func bigTags(size int) string {
	var b strings.Builder
	b.Grow(size + 16)
	b.WriteString(`{"tags":[{}`)
	for b.Len() < size {
		b.WriteString(",{}")
	}
	b.WriteString(`]}`)
	return b.String()
}

// hostile_bigout outputs about <input> bytes of bigTags.
//
//go:wasmexport hostile_bigout
func hostileBigout() int32 {
	size, _ := strconv.Atoi(string(pdk.Input()))
	pdk.OutputString(bigTags(size))
	return 0
}

// hostile_log writes <input> log lines of 64 KiB each.
//
//go:wasmexport hostile_log
func hostileLog() int32 {
	n, _ := strconv.Atoi(string(pdk.Input()))
	line := strings.Repeat("x", 64<<10)
	for range n {
		pdk.Log(pdk.LogInfo, line)
	}
	return 0
}

//go:wasmexport filter_render_head
func filterRenderHead() int32 { return misbehave() }

//go:wasmexport filter_jsonld_enrich
func filterJSONLDEnrich() int32 { return misbehave() }

//go:wasmexport filter_entity_before_save
func filterEntityBeforeSave() int32 { return misbehave() }

//go:wasmexport on_entity_published
func onEntityPublished() int32 { return misbehave() }

//go:wasmimport extism:host/user hm_call
func hmCall(offset uint64) uint64

// host_call forwards its input to hm_call and returns the response.
//
//go:wasmexport host_call
func hostCall() int32 {
	in := pdk.AllocateBytes(pdk.Input())
	out := pdk.FindMemory(hmCall(in.Offset()))
	pdk.Output(out.ReadBytes())
	return 0
}

// hostile_alloc_report is hostile_alloc that tells the host through hm_call
// how many MiB it holds after every chunk, so a test sees where the memory
// cap stopped it.
//
//go:wasmexport hostile_alloc_report
func hostileAllocReport() int32 {
	for {
		b := make([]byte, 1<<20)
		for i := range b {
			b[i] = 1
		}
		hoard = append(hoard, b)
		in := pdk.AllocateBytes([]byte(strconv.Itoa(len(hoard))))
		hmCall(in.Offset())
		in.Free()
	}
}

// version answers the manifest config "ver": which build of the plugin ran.
//
//go:wasmexport version
func version() int32 {
	v, _ := pdk.GetConfig("ver")
	pdk.OutputString(v)
	return 0
}
