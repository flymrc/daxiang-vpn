// Command contract-config-guard validates the currently supported sqlc generator
// configuration before the generator may write anything. Canonical options are
// still consumed by sqlc; new generators/paths require an explicit guard update.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type config struct {
	Version string `yaml:"version"`
	SQL     []struct {
		Engine  string `yaml:"engine"`
		Schema  string `yaml:"schema"`
		Queries string `yaml:"queries"`
		Gen     struct {
			Go struct {
				Package   string `yaml:"package"`
				Out       string `yaml:"out"`
				JSON      bool   `yaml:"emit_json_tags"`
				Prepared  bool   `yaml:"emit_prepared_queries"`
				Interface bool   `yaml:"emit_interface"`
				Exact     bool   `yaml:"emit_exact_table_names"`
			} `yaml:"go"`
		} `yaml:"gen"`
	} `yaml:"sql"`
}

func validate(data []byte) error {
	if len(data) == 0 || len(data) > 16<<10 {
		return fmt.Errorf("invalid config size")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	var c config
	if err := d.Decode(&c); err != nil {
		return fmt.Errorf("unsupported sqlc configuration")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple config documents")
	}
	if c.Version != "2" || len(c.SQL) != 1 {
		return fmt.Errorf("unsupported generator inventory")
	}
	s := c.SQL[0]
	if s.Engine != "sqlite" || s.Schema != "schema.sql" || s.Queries != "queries.sql" || s.Gen.Go.Out != "generated" ||
		!regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(s.Gen.Go.Package) {
		return fmt.Errorf("generator paths or package refused")
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "one canonical sqlc config is required")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "config unavailable")
		os.Exit(1)
	}
	data, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	_ = f.Close()
	if err != nil || validate(data) != nil {
		fmt.Fprintln(os.Stderr, "sqlc config refused before generation")
		os.Exit(1)
	}
}
