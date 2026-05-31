/*
Copyright 2024 The Crossplane Authors.

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

// Package validate implements offline schema validation of Crossplane resources.
package validate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/spf13/afero"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/version"

	"github.com/crossplane/cli/v2/cmd/crossplane/common/load"
	pkgvalidate "github.com/crossplane/cli/v2/cmd/crossplane/pkg/validate"
	"github.com/crossplane/cli/v2/cmd/crossplane/pkg/validate/render"

	_ "embed"
)

//go:embed help/validate.md
var helpDetail string

// errWriteOutput is the error message wrapped around I/O failures when the
// validate command writes to its output writer.
const errWriteOutput = "cannot write output"

// Cmd arguments and flags for render subcommand.
type Cmd struct {
	// Arguments.
	Extensions string `arg:"" help:"Extension sources as a comma-separated list of files, directories, or '-' for standard input."`
	Resources  string `arg:"" help:"Resource sources as a comma-separated list of files, directories, or '-' for standard input."`

	// Flags. Keep them in alphabetical order.
	CacheDir              string `default:"~/.crossplane/cache"                                        help:"Absolute path to the cache directory for downloaded schemas."                                                                                                                                                                                                 predictor:"directory"`
	CleanCache            bool   `help:"Clean the cache directory before downloading package schemas."`
	CrossplaneImage       string `help:"Specify the Crossplane image for validating built-in schemas."`
	ErrorOnMissingSchemas bool   `default:"false"                                                      help:"Return non zero exit code if missing schemas."`
	Output                string `default:"text"                                                       enum:"text,json,yaml"                                                                                                                                                                                                                                               help:"Output format for validation results (text, json, or yaml)." short:"o"`
	SkipSuccessResults    bool   `help:"Skip printing success results."`
	UpdateCache           bool   `default:"false"                                                      help:"Update cached schemas by downloading the latest version that satisfies a constraint. May be useful if you are using semantic version constraints and want to get the latest version, but this slows down the cache lookup due to the required network calls."`

	fs afero.Fs
}

// Help prints out the help for the validate command.
func (c *Cmd) Help() string {
	return helpDetail
}

// AfterApply implements kong.AfterApply.
func (c *Cmd) AfterApply() error {
	c.fs = afero.NewOsFs()
	return nil
}

// Run validate.
func (c *Cmd) Run(k *kong.Context, _ logging.Logger) error {
	if c.Resources == "-" && c.Extensions == "-" {
		return errors.New("cannot use stdin for both extensions and resources")
	}

	if len(c.CrossplaneImage) < 1 {
		c.CrossplaneImage = fmt.Sprintf("xpkg.crossplane.io/crossplane/crossplane:%s", version.New().GetVersionString())
	}

	// Load all extensions
	extensionLoader, err := load.NewLoader(c.Extensions)
	if err != nil {
		return errors.Wrapf(err, "cannot load extensions from %q", c.Extensions)
	}

	extensions, err := extensionLoader.Load()
	if err != nil {
		return errors.Wrapf(err, "cannot load extensions from %q", c.Extensions)
	}

	// Load all resources
	resourceLoader, err := load.NewLoader(c.Resources)
	if err != nil {
		return errors.Wrapf(err, "cannot load resources from %q", c.Resources)
	}

	resources, err := resourceLoader.Load()
	if err != nil {
		return errors.Wrapf(err, "cannot load resources from %q", c.Resources)
	}

	if strings.HasPrefix(c.CacheDir, "~/") {
		homeDir, _ := os.UserHomeDir()
		c.CacheDir = filepath.Join(homeDir, c.CacheDir[2:])
	}

	m := NewManager(c.CacheDir, c.fs, k.Stdout, WithCrossplaneImage(c.CrossplaneImage), WithUpdateCache(c.UpdateCache))

	// Convert XRDs/CRDs to CRDs and add package dependencies
	if err := m.PrepExtensions(extensions); err != nil {
		return errors.Wrapf(err, "cannot prepare extensions")
	}

	// Download package base layers to cache and load them as CRDs
	if err := m.CacheAndLoad(c.CleanCache); err != nil {
		return errors.Wrapf(err, "cannot download and load cache")
	}

	// Validate resources against schemas and render in the requested format.
	if err := c.validateAndRender(context.Background(), resources, m.crds, k.Stdout); err != nil {
		return errors.Wrapf(err, "cannot validate resources")
	}

	return nil
}

// validateAndRender runs schema validation on the given resources and CRDs,
// writes the result to w in the format configured on the Cmd, and returns a
// non-nil error when validation failed (or when ErrorOnMissingSchemas is set
// and any resource had no matching schema). It is the core of Cmd.Run,
// extracted so that tests can exercise the flag-driven behaviour without the
// file/cache loading machinery.
func (c *Cmd) validateAndRender(ctx context.Context, resources []*unstructured.Unstructured, crds []*extv1.CustomResourceDefinition, w io.Writer) error {
	result, err := pkgvalidate.SchemaValidate(ctx, resources, crds)
	if err != nil {
		return err
	}

	if err := render.RenderValidationResult(result, render.OutputFormat(c.Output), w, render.RenderOptions{SkipSuccessResults: c.SkipSuccessResults}); err != nil {
		return errors.Wrap(err, "cannot render validation result")
	}

	return pkgvalidate.ResultError(result, c.ErrorOnMissingSchemas)
}
