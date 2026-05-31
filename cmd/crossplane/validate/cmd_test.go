/*
Copyright 2026 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/yaml"

	pkgvalidate "github.com/crossplane/cli/v2/cmd/crossplane/pkg/validate"
)

// parseCmdFlags exercises Kong's parsing of the Cmd struct tags. It builds a
// Cmd with fake positional args so we can focus on flag behaviour.
func parseCmdFlags(t *testing.T, args ...string) *Cmd {
	t.Helper()
	c := &Cmd{}
	// Positional args Extensions and Resources are required; supply dummies.
	full := append([]string{"ext.yaml", "res.yaml"}, args...)
	parser, err := kong.New(c)
	if err != nil {
		t.Fatalf("kong.New(): %v", err)
	}
	if _, err := parser.Parse(full); err != nil {
		t.Fatalf("kong.Parse(%v): %v", full, err)
	}
	return c
}

func TestCmd_OutputFlagParsing(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"default":    {args: nil, want: "text"},
		"long_text":  {args: []string{"--output=text"}, want: "text"},
		"long_json":  {args: []string{"--output=json"}, want: "json"},
		"long_yaml":  {args: []string{"--output=yaml"}, want: "yaml"},
		"short_flag": {args: []string{"-o", "json"}, want: "json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := parseCmdFlags(t, tc.args...)
			if c.Output != tc.want {
				t.Errorf("Cmd.Output = %q; want %q", c.Output, tc.want)
			}
		})
	}
}

func TestCmd_OutputFlagRejectsUnknown(t *testing.T) {
	c := &Cmd{}
	parser, err := kong.New(c)
	if err != nil {
		t.Fatalf("kong.New(): %v", err)
	}
	if _, err := parser.Parse([]string{"ext.yaml", "res.yaml", "--output=xml"}); err == nil {
		t.Errorf("kong.Parse(--output=xml) = nil; want non-nil enum-validation error")
	}
}

// invalidFixture mirrors the `Invalid` case from the backward-compat test —
// a resource whose replicas field is the wrong type.
func invalidFixture() ([]*unstructured.Unstructured, []*extv1.CustomResourceDefinition) {
	return []*unstructured.Unstructured{{Object: map[string]any{
			"apiVersion": "test.org/v1alpha1",
			"kind":       "Test",
			"metadata":   map[string]any{"name": "test"},
			"spec":       map[string]any{"replicas": "not-an-int"},
		}}},
		[]*extv1.CustomResourceDefinition{testCRD}
}

func validFixture() ([]*unstructured.Unstructured, []*extv1.CustomResourceDefinition) {
	return []*unstructured.Unstructured{{Object: map[string]any{
			"apiVersion": "test.org/v1alpha1",
			"kind":       "Test",
			"metadata":   map[string]any{"name": "test"},
			"spec":       map[string]any{"replicas": 1},
		}}},
		[]*extv1.CustomResourceDefinition{testCRD}
}

func missingFixture() ([]*unstructured.Unstructured, []*extv1.CustomResourceDefinition) {
	return []*unstructured.Unstructured{{Object: map[string]any{
			"apiVersion": "test.org/v1alpha1",
			"kind":       "Test",
			"metadata":   map[string]any{"name": "test"},
			"spec":       map[string]any{"replicas": 1},
		}}},
		[]*extv1.CustomResourceDefinition{}
}

func TestCmd_ValidateAndRender(t *testing.T) {
	type fixture func() ([]*unstructured.Unstructured, []*extv1.CustomResourceDefinition)
	type want struct {
		stdoutSubstr string
		wantErr      bool
		parseAs      string // "json", "yaml", or "" for no structural parse
	}

	cases := map[string]struct {
		output                string
		errorOnMissingSchemas bool
		skipSuccess           bool
		fix                   fixture
		want                  want
	}{
		"default_text_valid": {
			output: "text", fix: validFixture,
			want: want{stdoutSubstr: "[✓] test.org/v1alpha1, Kind=Test, test validated successfully"},
		},
		"output_text_explicit_invalid": {
			output: "text", fix: invalidFixture,
			want: want{stdoutSubstr: "[x] schema validation error", wantErr: true},
		},
		"output_json_valid": {
			output: "json", fix: validFixture,
			want: want{parseAs: "json"},
		},
		"output_yaml_valid": {
			output: "yaml", fix: validFixture,
			want: want{parseAs: "yaml"},
		},
		"output_json_invalid_exits_nonzero": {
			output: "json", fix: invalidFixture,
			want: want{parseAs: "json", wantErr: true},
		},
		"output_json_missing_with_flag": {
			output: "json", errorOnMissingSchemas: true, fix: missingFixture,
			want: want{parseAs: "json", wantErr: true},
		},
		"output_json_missing_without_flag": {
			output: "json", errorOnMissingSchemas: false, fix: missingFixture,
			want: want{parseAs: "json"},
		},
		"skip_success_with_json_still_has_full_payload": {
			output: "json", skipSuccess: true, fix: validFixture,
			want: want{parseAs: "json"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &Cmd{Output: tc.output, ErrorOnMissingSchemas: tc.errorOnMissingSchemas, SkipSuccessResults: tc.skipSuccess}
			resources, crds := tc.fix()
			var buf bytes.Buffer
			err := c.validateAndRender(context.Background(), resources, crds, &buf)
			if (err != nil) != tc.want.wantErr {
				t.Errorf("validateAndRender() err = %v; wantErr = %v", err, tc.want.wantErr)
			}

			if tc.want.stdoutSubstr != "" && !strings.Contains(buf.String(), tc.want.stdoutSubstr) {
				t.Errorf("stdout missing substring %q\n--- got ---\n%s", tc.want.stdoutSubstr, buf.String())
			}

			switch tc.want.parseAs {
			case "json":
				var got pkgvalidate.ValidationResult
				if e := json.Unmarshal(buf.Bytes(), &got); e != nil {
					t.Fatalf("stdout is not valid JSON: %v\n%s", e, buf.String())
				}
				if got.Summary.Total == 0 {
					t.Errorf("JSON result.Summary.Total == 0; fixture had resources")
				}
				// For skip_success case, verify Valid results are still included.
				if tc.skipSuccess && got.Summary.Valid == 0 {
					t.Errorf("--skip-success-results must not strip valid entries from JSON payload; got %+v", got)
				}
			case "yaml":
				var got pkgvalidate.ValidationResult
				if e := yaml.Unmarshal(buf.Bytes(), &got); e != nil {
					t.Fatalf("stdout is not valid YAML: %v\n%s", e, buf.String())
				}
				if got.Summary.Total == 0 {
					t.Errorf("YAML result.Summary.Total == 0; fixture had resources")
				}
			}
		})
	}
}
