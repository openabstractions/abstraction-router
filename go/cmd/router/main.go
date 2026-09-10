package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	router "github.com/openabstractions/service-router/go"
)

const usage = `router models              what models exist here, under every name
router residency           which host holds which, and what that costs
router route <model>       which host should serve a request

-hosts a,b   before the op: only these may serve it. -hosts= authorises none,
             and the flag left off authorises every servable host.`

func main() {
	endpoint := flag.String("endpoint", router.DefaultEndpoint(), "where routerd listens")
	fresh := flag.Bool("fresh", false, "read the hosts now instead of the last survey")
	hosts := flag.String("hosts", "", "hosts this request authorises; unset authorises every servable host")
	flag.Parse()

	req := router.Request{Op: flag.Arg(0), Model: flag.Arg(1), Fresh: *fresh}
	flag.Visit(func(f *flag.Flag) {
		if f.Name != "hosts" {
			return
		}
		list := []string{}
		if *hosts != "" {
			list = strings.Split(*hosts, ",")
		}
		req.Hosts = &list
	})
	if req.Op == "" || (req.Op == router.OpRoute && req.Model == "") {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	c := &router.Client{Endpoint: *endpoint}
	out, err := c.Ask(req)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	if err != nil {
		os.Exit(1)
	}
}
