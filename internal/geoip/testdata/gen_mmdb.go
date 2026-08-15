//go:build ignore

// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// gen_mmdb.go regenerates country-test.mmdb, the fixture that lets
// internal/geoip exercise a real maxminddb Lookup/Decode. GeoLite2 needs a
// MaxMind licence and the library keeps its own test databases in a git
// submodule, so neither is available to this repository's tests.
//
// Run it from a throwaway module so mmdbwriter never enters ocms-go's go.mod:
//
//	mkdir -p /tmp/mmdbgen && cd /tmp/mmdbgen
//	go mod init mmdbgen
//	go get github.com/maxmind/mmdbwriter@v1.2.0
//	cp <repo>/internal/geoip/testdata/gen_mmdb.go .
//	go run gen_mmdb.go -out <repo>/internal/geoip/testdata/country-test.mmdb
//
// Keep the networks below in sync with geoip_test.go. 81.2.69.142 is MaxMind's
// own documentation address for GB; 1.1.1.1 is deliberately absent so the test
// can assert that a lookup miss yields an empty country code rather than an
// error.
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func main() {
	out := flag.String("out", "country-test.mmdb", "path to write the database to")
	flag.Parse()

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "GeoLite2-Country",
		RecordSize:   24,
		IPVersion:    6,
		Languages:    []string{"en"},
		Description:  map[string]string{"en": "ocms-go test fixture, not real geolocation data"},
	})
	if err != nil {
		log.Fatalf("creating tree: %v", err)
	}

	entries := []struct{ cidr, iso string }{
		{"81.2.69.142/32", "GB"},
		{"8.8.8.8/32", "US"},
		{"2606:4700::/32", "US"},
	}
	for _, e := range entries {
		_, network, perr := net.ParseCIDR(e.cidr)
		if perr != nil {
			log.Fatalf("parsing %s: %v", e.cidr, perr)
		}
		record := mmdbtype.Map{
			"country": mmdbtype.Map{"iso_code": mmdbtype.String(e.iso)},
		}
		if ierr := tree.Insert(network, record); ierr != nil {
			log.Fatalf("inserting %s: %v", e.cidr, ierr)
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("creating %s: %v", *out, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			log.Fatalf("closing %s: %v", *out, cerr)
		}
	}()
	n, err := tree.WriteTo(f)
	if err != nil {
		log.Fatalf("writing %s: %v", *out, err)
	}
	log.Printf("wrote %s (%d bytes)", *out, n)
}
