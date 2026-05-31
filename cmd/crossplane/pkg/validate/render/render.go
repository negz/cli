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

package render

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"

	pkgvalidate "github.com/crossplane/cli/v2/cmd/crossplane/pkg/validate"
)

const (
	errCannotMarshalJSON = "cannot marshal validation result as JSON"
	errCannotMarshalYAML = "cannot marshal validation result as YAML"
	errUnknownFormat     = "unknown output format"
)

// OutputFormat specifies how validation results should be rendered.
type OutputFormat string

// OutputFormat values.
const (
	// OutputFormatText renders results in human-readable text format with
	// [x], [!], [✓] markers.
	OutputFormatText OutputFormat = "text"
	// OutputFormatJSON renders results as JSON.
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatYAML renders results as YAML.
	OutputFormatYAML OutputFormat = "yaml"
)

// RenderOptions configures how a validation result is rendered.
type RenderOptions struct {
	// SkipSuccessResults suppresses per-resource success lines in text output.
	// It has no effect on JSON or YAML output.
	SkipSuccessResults bool
}

// RenderValidationResult writes the validation result to w in the requested
// format. An unknown format returns an error without writing to w.
func RenderValidationResult(result *pkgvalidate.ValidationResult, format OutputFormat, w io.Writer, opts RenderOptions) error {
	switch format {
	case OutputFormatJSON:
		return renderJSON(result, w)
	case OutputFormatYAML:
		return renderYAML(result, w)
	case OutputFormatText, "":
		return renderText(result, w, opts)
	default:
		return errors.Errorf("%s: %q", errUnknownFormat, format)
	}
}

func renderJSON(result *pkgvalidate.ValidationResult, w io.Writer) error {
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.Wrap(err, errCannotMarshalJSON)
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}

func renderYAML(result *pkgvalidate.ValidationResult, w io.Writer) error {
	out, err := yaml.Marshal(result)
	if err != nil {
		return errors.Wrap(err, errCannotMarshalYAML)
	}
	_, err = fmt.Fprint(w, string(out))
	return err
}

// renderText writes the result in the human-readable format that the
// validate CLI has historically produced, preserving line order and
// prefixes ([!], [x], [✓]).
func renderText(result *pkgvalidate.ValidationResult, w io.Writer, opts RenderOptions) error {
	for _, r := range result.Resources {
		gvk := fmt.Sprintf("%s, Kind=%s", r.APIVersion, r.Kind)
		switch r.Status {
		case pkgvalidate.ValidationStatusMissingSchema:
			if _, err := fmt.Fprintf(w, "[!] could not find CRD/XRD for: %s\n", gvk); err != nil {
				return err
			}
		case pkgvalidate.ValidationStatusDefaultingFailed:
			for _, e := range r.Errors {
				if e.Type == pkgvalidate.FieldErrorTypeDefaulting {
					if _, err := fmt.Fprintf(w, "[!] failed to apply defaults for %s, %s: %s\n", gvk, r.Name, e.Message); err != nil {
						return err
					}
				}
			}
		case pkgvalidate.ValidationStatusInvalid:
			for _, e := range r.Errors {
				prefix := "schema validation error"
				if e.Type == pkgvalidate.FieldErrorTypeCEL {
					prefix = "CEL validation error"
				}
				if _, err := fmt.Fprintf(w, "[x] %s %s, %s : %s\n", prefix, gvk, r.Name, e.Message); err != nil {
					return err
				}
			}
		case pkgvalidate.ValidationStatusValid:
			if opts.SkipSuccessResults {
				continue
			}
			if _, err := fmt.Fprintf(w, "[✓] %s, %s validated successfully\n", gvk, r.Name); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(w, "Total %d resources: %d missing schemas, %d success cases, %d failure cases\n",
		result.Summary.Total, result.Summary.MissingSchemas, result.Summary.Valid, result.Summary.Invalid); err != nil {
		return err
	}
	return nil
}
