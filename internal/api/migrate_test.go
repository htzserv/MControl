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

func TestExtractBackhaulRatholePaqetAndKernel(t *testing.T) {
	d := parseLegacyDump(dumpOf(map[string]string{
		"/etc/mbackhaul/tunnels/bh1.meta":      "ROLE=1\nTRANSPORT=tcpmux\nTUN_PORT=3080\nREMOTE_IP=\nTOKEN=abc\nPORTS=443,8443\nPEER_SETUP_ID=p99\n",
		"/etc/mbackhaul/tunnels/bh1.toml":      "[server]\nbind_addr = \"0.0.0.0:3080\"\n",
		"/etc/mrathole/tunnels/r1/meta.conf":   "TYPE=2\nLINK_PORT=7000\nREMOTE_IP=1.2.3.4\nTOKEN=tok\nTCP_PORTS=80,443\nUDP_PORTS=53\n",
		"/etc/mrathole/tunnels/r1/config.toml": "[client]\nremote_addr = \"1.2.3.4:7000\"\n",
		"/etc/paqet/p_srv.meta":                "ROLE=1\nTUN_PORT=9000\nREMOTE_IP=0.0.0.0\nTCP_PORTS=\n",
		"/etc/paqet/p_srv.yaml":                "role: \"server\"\nlisten:\n  addr: \":9000\"\ntransport:\n  kcp:\n    key: \"secretkey\"\n",
		"/etc/paqet/p_cli.meta":                "ROLE=2\nTUN_PORT=9000\nREMOTE_IP=9.9.9.9\nTCP_PORTS=443,8443\n",
		"/etc/paqet/p_cli.yaml":                "role: \"client\"\nforward:\n  - listen: \"0.0.0.0:443\"\n  - listen: \"0.0.0.0:8443\"\ntransport:\n  kcp:\n    key: \"secretkey\"\n",
		"/etc/mgre/tunnels/g1.conf":            "TYPE=1\nSYNC_KEY=zz\nTUN_PROTO=gre6\nCORE_SUBNET=10.10.20\nFWD_TCP=80\nENCRYPT=1\n",
		"/etc/mporter/iptables_core/rules.sh":  "# rules\n",
		"/etc/mbackhaul/tunnels/$(evil).meta":  "ROLE=1\nTUN_PORT=1\n",
		"/etc/mbackhaul/tunnels/$(evil).toml":  "[server]\n",
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
	if bh.Side != "iran" || bh.TunnelPort != 3080 || bh.Token != "abc" || bh.PairKey != "p99" || bh.Ports != "443,8443" || bh.Status != "pending" {
		t.Fatalf("backhaul parsed wrong: %+v", bh)
	}
	rh := tuns[by["rathole/r1"]]
	if rh.Side != "foreign" || rh.PeerHost != "1.2.3.4" || rh.TunnelPort != 7000 || rh.Ports != "80,443,53" {
		t.Fatalf("rathole parsed wrong: %+v", rh)
	}
	pqSrv := tuns[by["paqet/p_srv"]]
	if pqSrv.Side != "foreign" || pqSrv.Token != "secretkey" || pqSrv.PairKey != "secretkey" {
		t.Fatalf("paqet server (ROLE=1) must map to foreign side: %+v", pqSrv)
	}
	pqCli := tuns[by["paqet/p_cli"]]
	if pqCli.Side != "iran" || pqCli.Ports != "443,8443" || pqCli.PeerHost != "9.9.9.9" {
		t.Fatalf("paqet client (ROLE=2) must map to iran side: %+v", pqCli)
	}
	gr := tuns[by["gre/g1"]]
	if gr.Status != "kernel_kept" || gr.Transport != "gre6" || !strings.Contains(gr.Note, "IPsec ESP") {
		t.Fatalf("kernel tunnel parsed wrong: %+v", gr)
	}
	if tuns[by["backhaul/$(evil)"]].Status != "error" {
		t.Fatalf("unsafe tunnel name must not be deployable")
	}
	if len(unsupported) != 1 || !strings.Contains(unsupported[0], "MPorter") {
		t.Fatalf("expected only MPorter in unsupported, got %v", unsupported)
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

func TestVerbatimDeployScriptPaqetIncludesIptables(t *testing.T) {
	cfg := "role: \"server\"\nlisten:\n  addr: \":9000\"\nkcp:\n  key: x\n"
	ports := extractPaqetIptablesPorts(cfg, "", 9000)
	s := verbatimDeployScript("paqet", "mcontrol-tun-5", "p1", 5, cfg, nil, ports...)
	for _, want := range []string{
		"run -c /etc/mcontrol/tunnels/mcontrol-tun-5.yaml",
		"NOTRACK -m comment --comment mc_tun_5",
		"--tcp-flags RST RST -j DROP -m comment --comment mc_tun_5",
		"mcontrol-tun-5.service",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script misses %q:\n%s", want, s)
		}
	}
}
