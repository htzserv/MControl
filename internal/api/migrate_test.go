package api

import (
	"encoding/base64"
	"strings"
	"testing"
)

func dumpOf(files map[string]string) string {
	var b strings.Builder
	b.WriteString("@@HAS /etc/mbackhaul\n@@IP4 5.6.7.8\n@@IP6 \n")
	for p, c := range files {
		b.WriteString("@@FILE " + p + "\n" + base64.StdEncoding.EncodeToString([]byte(c)) + "\n@@END\n")
	}
	b.WriteString("@@DONE found=1\n")
	return b.String()
}

func TestExtractBackhaulRatholePaqet(t *testing.T) {
	d := parseLegacyDump(dumpOf(map[string]string{
		"/etc/mbackhaul/tunnels/bh1.meta":        "ROLE=1\nTRANSPORT=tcpmux\nTUN_PORT=3080\nREMOTE_IP=\nTOKEN=abc\nPORTS=443,8443\n",
		"/etc/mbackhaul/tunnels/bh1.toml":        "[server]\nbind_addr = \"0.0.0.0:3080\"\n",
		"/etc/mrathole/tunnels/r1/meta.conf":     "TYPE=2\nLINK_PORT=7000\nREMOTE_IP=1.2.3.4\nTOKEN=tok\nTCP_PORTS=80,443\nUDP_PORTS=\n",
		"/etc/mrathole/tunnels/r1/config.toml":   "[client]\nremote_addr = \"1.2.3.4:7000\"\n",
		"/etc/paqet/p1.meta":                     "ROLE=1\nTUN_PORT=9000\nREMOTE_IP=0.0.0.0\nTCP_PORTS=22\n",
		"/etc/paqet/p1.yaml":                     "kcp:\n  key: \"secretkey\"\n",
		"/etc/mgre/tunnels/g1.conf":              "TYPE=1\nSYNC_KEY=zz\nFWD_TCP=80\n",
		"/etc/mporter/iptables_core/rules.sh":    "# rules\n",
		"/etc/mbackhaul/tunnels/$(evil).meta":    "ROLE=1\nTUN_PORT=1\n",
		"/etc/mbackhaul/tunnels/$(evil).toml":    "[server]\n",
	}))
	if !d.Found || d.IPv4 != "5.6.7.8" {
		t.Fatalf("dump not parsed: %+v", d)
	}
	tuns, unsupported := extractLegacyTunnels(7, d)
	by := map[string]int{}
	for i, x := range tuns {
		by[x.Engine+"/"+x.Name] = i
	}
	bh := tuns[by["backhaul/bh1"]]
	if bh.Side != "iran" || bh.TunnelPort != 3080 || bh.Token != "abc" || bh.Ports != "443,8443" || bh.Status != "pending" {
		t.Fatalf("backhaul parsed wrong: %+v", bh)
	}
	if !strings.Contains(bh.Config, "bind_addr") {
		t.Fatalf("backhaul config not kept verbatim")
	}
	rh := tuns[by["rathole/r1"]]
	if rh.Side != "foreign" || rh.PeerHost != "1.2.3.4" || rh.TunnelPort != 7000 {
		t.Fatalf("rathole parsed wrong: %+v", rh)
	}
	pq := tuns[by["paqet/p1"]]
	if pq.Token != "secretkey" || pq.PairKey != "secretkey" || pq.Side != "iran" {
		t.Fatalf("paqet parsed wrong: %+v", pq)
	}
	if tuns[by["gre/g1"]].Status != "snapshot_only" {
		t.Fatalf("kernel tunnel must stay snapshot_only")
	}
	if tuns[by["backhaul/$(evil)"]].Status != "error" {
		t.Fatalf("unsafe tunnel name must not be deployable")
	}
	if len(unsupported) < 2 {
		t.Fatalf("expected GRE + MPorter in unsupported, got %v", unsupported)
	}
}

func TestPrepareConfigRewritesCertPaths(t *testing.T) {
	cfg := "tls_cert = \"/etc/mbackhaul/certs/a.crt\"\ntls_key = \"/etc/mbackhaul/certs/a.key\"\n"
	out, certs := prepareConfig(cfg, map[string]string{
		"/etc/mbackhaul/certs/a.crt": "CRT",
		"/etc/mbackhaul/certs/a.key": "KEY",
	})
	if strings.Contains(out, "/etc/mbackhaul/") || certs["a.crt"] != "CRT" || certs["a.key"] != "KEY" {
		t.Fatalf("cert paths not rewritten: %q %v", out, certs)
	}
}

func TestVerbatimDeployScriptUsesOriginalConfig(t *testing.T) {
	s := verbatimDeployScript("paqet", "mcontrol-tun-5", "p1", 5, "kcp:\n  key: x\n", nil)
	for _, want := range []string{"run -c /etc/mcontrol/tunnels/mcontrol-tun-5.yaml", "kcp:", "mcontrol-tun-5.service"} {
		if !strings.Contains(s, want) {
			t.Fatalf("script misses %q:\n%s", want, s)
		}
	}
}
