// Package router answers three questions about the model hosts already running
// on one machine, and nothing else.
//
//	what models exist here, under every name they are known by
//	which host holds which, and what that costs
//	given a request, which host should serve it
//
// It hosts no model, ships no chat client and renders no interface. The three
// measurements it is built on say why. Consolidating three models into one host
// saved 0.30 GB of 42.87 and turned one failed load from a 33% loss into a 100%
// one; not loading the same model twice saved 22.52 GB with every host left
// exactly where it was; and four hosts name the same weights four ways with no
// overlap, so the resolution cannot live in any of them.
package router

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/openabstractions/abstraction-model/go/identity"
)

type snapshot struct {
	at       time.Time
	hosts    []HostState
	families []Family
	pick     map[string]map[string]string
	gpu      []Holder
	gpuWhy   string
}

type Router struct {
	hosts []*Host
	mu    sync.Mutex
	snap  snapshot
	asked []Ask
}

func New(hosts ...*Host) *Router { return &Router{hosts: hosts} }

// Survey reads every host once, in parallel. It is the only thing here that
// touches a host, and it only reads.
func (r *Router) Survey() {
	type read struct {
		state     HostState
		installed []string
		resident  []string
	}
	reads := make([]read, len(r.hosts))
	var wg sync.WaitGroup
	for i, h := range r.hosts {
		wg.Add(1)
		go func(i int, h *Host) {
			defer wg.Done()
			s := HostState{Host: h.Name, Base: h.Base, Servable: h.Servable()}
			installed, err := h.installed(h)
			if err != nil {
				s.Why = err.Error()
				reads[i] = read{state: s}
				return
			}
			resident, err := h.resident(h)
			if err != nil {
				s.Why = err.Error()
				reads[i] = read{state: s}
				return
			}
			s.Up, s.Installed, s.Resident = true, len(installed), resident
			reads[i] = read{state: s, installed: installed, resident: resident}
		}(i, h)
	}
	wg.Wait()

	byFamily := map[string]map[string]*Alias{}
	pick := map[string]map[string]string{}
	for i, h := range r.hosts {
		note := func(name string, resident bool) {
			fam := identity.Family(name)
			if fam == "" {
				return
			}
			names := byFamily[fam]
			if names == nil {
				names = map[string]*Alias{}
				byFamily[fam] = names
			}
			if a, seen := names[name]; seen {
				a.Resident = a.Resident || resident
			} else {
				names[name] = &Alias{Host: h.Name, Name: name, Resident: resident, Servable: h.Servable()}
			}
			if pick[fam] == nil {
				pick[fam] = map[string]string{}
			}
			// A host carries several builds of one family — llama.cpp, an MTP
			// variant, an NPU recipe. A build it already holds wins, because
			// loading a second build of a model the host has in memory is the
			// waste this exists to stop; otherwise the host's own order decides.
			if _, taken := pick[fam][h.Name]; !taken || resident {
				pick[fam][h.Name] = name
			}
		}
		for _, name := range reads[i].resident {
			note(name, true)
		}
		for _, name := range reads[i].installed {
			note(name, false)
		}
	}

	families := make([]Family, 0, len(byFamily))
	for fam, names := range byFamily {
		f := Family{Family: fam}
		for _, a := range names {
			f.Names = append(f.Names, *a)
		}
		sort.Slice(f.Names, func(i, j int) bool { return f.Names[i].Name < f.Names[j].Name })
		families = append(families, f)
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Family < families[j].Family })

	gpu, err := gpuHolders()
	why := ""
	if err != nil {
		why = err.Error()
	}
	states := make([]HostState, len(reads))
	for i := range reads {
		states[i] = reads[i].state
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.snap = snapshot{at: time.Now(), hosts: states, families: families, pick: pick, gpu: gpu, gpuWhy: why}
}

func (r *Router) latest(fresh bool) snapshot {
	if fresh {
		r.Survey()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

// Poll re-surveys until stop is closed. A host publishes no notification when a
// model is loaded, so the only honest options are to ask on every request or to
// ask on a clock; asking on a clock is what keeps an answer inside the budget,
// and every answer carries the age of what it was read from.
func (r *Router) Poll(every time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		r.Survey()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (r *Router) Models(fresh bool) ([]Family, time.Time) {
	s := r.latest(fresh)
	return s.families, s.at
}

func (r *Router) Residency(fresh bool) ([]HostState, []Holder, string, []string, time.Time) {
	s := r.latest(fresh)
	return s.hosts, s.gpu, s.gpuWhy, doubled(s), s.at
}

// doubled names a family held by more than one host while it is happening. The
// identity judgement will be wrong eventually; when it is, the machine holds two
// copies and nothing else on it says so.
func doubled(s snapshot) []string {
	count := map[string]int{}
	for _, f := range s.families {
		for _, a := range f.Names {
			if a.Resident {
				count[f.Family+"\x00"+a.Host]++
			}
		}
	}
	hosts := map[string][]string{}
	for k := range count {
		fam, host, _ := cut(k)
		hosts[fam] = append(hosts[fam], host)
	}
	var out []string
	for fam, hs := range hosts {
		if len(hs) > 1 {
			sort.Strings(hs)
			out = append(out, fmt.Sprintf("%s is loaded %d times: %v", fam, len(hs), hs))
		}
	}
	sort.Strings(out)
	return out
}

func cut(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}

// Route decides. It never loads: a verdict of would-load names the host that
// should be asked and leaves the asking to the caller, so the router cannot
// become the thing that holds weights.
func (r *Router) Route(req Request) (*Decision, time.Time) {
	s := r.latest(req.Fresh)
	fam := identity.Family(req.Model)
	d := &Decision{Asked: req.Model, Family: fam, Authorised: req.Hosts}
	if fam == "" {
		d.Verdict, d.Why = EmptyFamily, "nothing in "+quote(req.Model)+" names a model"
		return d, s.at
	}
	var installedOn []string
	for _, f := range s.families {
		if f.Family != fam {
			continue
		}
		for _, a := range f.Names {
			if !has(installedOn, a.Host) {
				installedOn = append(installedOn, a.Host)
			}
		}
	}
	d.InstalledOn = installedOn

	for _, c := range r.candidates(s, fam) {
		if !d.permits(c.host.Name) {
			d.Withheld = append(d.Withheld, c.host.Name)
			continue
		}
		d.Host, d.Model, d.Endpoint = c.host.Name, c.name, c.host.Endpoint()
		if c.held {
			d.Verdict, d.Loads = Resident, 0
			d.Why = c.host.Name + " already holds it; routing anywhere else loads a second copy"
		} else {
			d.Verdict, d.Loads = WouldLoad, 1
			d.Why = "no host holds it; " + c.host.Name + " has it installed. The router does not load it"
		}
		return d, s.at
	}
	if len(d.Withheld) > 0 {
		d.Verdict = Unauthorised
		d.Why = fmt.Sprintf("%v can serve it and the request authorised %v; the router does not go outside what it was given",
			d.Withheld, *d.Authorised)
		return d, s.at
	}
	if len(installedOn) > 0 {
		d.Verdict = Unservable
		d.Why = fmt.Sprintf("only %v has it, and what that runs is a graph, not a model an OpenAI-compatible endpoint can serve", installedOn)
		return d, s.at
	}
	d.Verdict, d.Why = NotOnDisk, "no host on this machine has it installed"
	return d, s.at
}

func (d *Decision) permits(host string) bool {
	return d.Authorised == nil || has(*d.Authorised, host)
}

type candidate struct {
	host *Host
	name string
	held bool
}

// candidates is every host that could serve the family, best first: each host
// that already holds it, then each host that has it on disk. A host that holds
// it beats one that would load it however the request orders them, because a
// second copy of a model already in memory is the waste this exists to stop.
func (r *Router) candidates(s snapshot, fam string) []candidate {
	var out []candidate
	for _, h := range r.hosts {
		if !h.Servable() {
			continue
		}
		if names := residentOf(s, fam, h.Name); len(names) > 0 {
			out = append(out, candidate{host: h, name: names[0], held: true})
		}
	}
	for _, h := range r.hosts {
		if !h.Servable() || len(residentOf(s, fam, h.Name)) > 0 {
			continue
		}
		if name, ok := s.pick[fam][h.Name]; ok {
			out = append(out, candidate{host: h, name: name})
		}
	}
	return out
}

func residentOf(s snapshot, fam, host string) []string {
	var out []string
	for _, f := range s.families {
		if f.Family != fam {
			continue
		}
		for _, a := range f.Names {
			if a.Resident && a.Host == host {
				out = append(out, a.Name)
			}
		}
	}
	return out
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func (r *Router) record(a Ask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append([]Ask{a}, r.asked...)
	if len(r.asked) > 50 {
		r.asked = r.asked[:50]
	}
}

// Asked is the audit no host can produce: which program asked for which model,
// and what it was sent to. A host sees a request; only something above all of
// them sees who caused a load.
func (r *Router) Asked() []Ask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Ask(nil), r.asked...)
}
