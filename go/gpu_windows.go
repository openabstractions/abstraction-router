package router

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Every runtime on this machine reports GPU memory per process and so calls the
// GPU empty while another process holds 22 GB of it; three instruments gave
// three answers at one instant and two of them were wrong by tens of gigabytes.
// These counters are the only instrument here that sees the whole device.
var (
	pdh         = syscall.NewLazyDLL("pdh.dll")
	openQuery   = pdh.NewProc("PdhOpenQueryW")
	addCounter  = pdh.NewProc("PdhAddEnglishCounterW")
	collect     = pdh.NewProc("PdhCollectQueryData")
	formatArray = pdh.NewProc("PdhGetFormattedCounterArrayW")
	closeQuery  = pdh.NewProc("PdhCloseQuery")
)

const (
	fmtLarge = 0x400
	moreData = 0x800007D2
)

type item struct {
	name   *uint16
	status uint32
	_      uint32
	value  int64
}

func counters(paths ...string) ([]sample, error) {
	var q uintptr
	if r, _, _ := openQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r != 0 {
		return nil, errors.New("PdhOpenQueryW refused")
	}
	defer closeQuery.Call(q)
	var hs []uintptr
	for _, p := range paths {
		w, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			return nil, err
		}
		var c uintptr
		if r, _, _ := addCounter.Call(q, uintptr(unsafe.Pointer(w)), 0, uintptr(unsafe.Pointer(&c))); r == 0 {
			hs = append(hs, c)
		}
	}
	if len(hs) == 0 {
		return nil, errors.New("no counter path could be added")
	}
	// Twice. The first collect in a process enumerates the counterset and
	// returns a partial set of GPU instances: it reported the process holding
	// this machine's 24 GB model as holding 2.81 GB, and the same query a
	// moment later reported 24.01. A number that is wrong by an order of
	// magnitude on the first reading is worse than no number.
	for range 2 {
		if r, _, _ := collect.Call(q); r != 0 {
			return nil, errors.New("PdhCollectQueryData refused")
		}
	}
	var out []sample
	for _, c := range hs {
		out = append(out, array(c)...)
	}
	return out, nil
}

// A sample stays a pair rather than being folded into a map keyed by instance
// name: two instances can carry one name, and summing them turned a process id
// into a number no process has, which left the process holding 46 GB unnamed.
type sample struct {
	instance string
	value    int64
}

func array(c uintptr) []sample {
	var size, count uint32
	r, _, _ := formatArray.Call(c, fmtLarge, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(r) != moreData {
		return nil
	}
	buf := make([]byte, size)
	if r, _, _ := formatArray.Call(c, fmtLarge, uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return nil
	}
	items := unsafe.Slice((*item)(unsafe.Pointer(&buf[0])), count)
	out := make([]sample, 0, count)
	for _, it := range items {
		out = append(out, sample{utf16String(it.name), it.value})
	}
	return out
}

func gpuHolders() ([]Holder, error) {
	mem, err := counters(`\GPU Process Memory(*)\Local Usage`, `\GPU Process Memory(*)\Dedicated Usage`)
	if err != nil {
		return nil, err
	}
	named, _ := counters(`\Process(*)\ID Process`)
	byPID := map[int]string{}
	for _, s := range named {
		byPID[int(s.value)] = s.instance
	}
	total := map[int]int64{}
	for _, s := range mem {
		_, rest, ok := strings.Cut(s.instance, "pid_")
		if !ok {
			continue
		}
		digits, _, _ := strings.Cut(rest, "_")
		if pid, err := strconv.Atoi(digits); err == nil {
			total[pid] += s.value
		}
	}
	var out []Holder
	for pid, v := range total {
		if v < 256<<20 {
			continue
		}
		name := byPID[pid]
		if name == "" {
			name = "pid" + strconv.Itoa(pid)
		}
		out = append(out, Holder{Process: name, PID: pid, GiB: round(float64(v) / (1 << 30))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GiB > out[j].GiB })
	return out, nil
}

func utf16String(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for q := unsafe.Pointer(p); *(*uint16)(q) != 0; q = unsafe.Add(q, 2) {
		n++
	}
	return string(utf16.Decode(unsafe.Slice(p, n)))
}
