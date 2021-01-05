package lint

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/v3/pkg/files"
	"github.com/jenkins-x/jx-helpers/v3/pkg/linter"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"github.com/jenkins-x/jx-helpers/v3/pkg/yamls"
	"github.com/jenkins-x/lighthouse/pkg/config/job"
	"github.com/jenkins-x/lighthouse/pkg/triggerconfig"
	"github.com/jenkins-x/lighthouse/pkg/triggerconfig/inrepo"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Options contains the command line options
type Options struct {
	linter.Options

	Dir       string
	Namespace string
	OutFile   string
	Format    string
	Recursive bool
}

var (
	info = termcolor.ColorInfo

	cmdLong = templates.LongDesc(`
		Lints the lighthouse trigger and tekton pipelines
`)

	cmdExample = templates.Examples(`
		# Lints the lighthouse files and local pipeline files
		jx pipeline lint
	`)
)

// NewCmdPipelineLint creates the command
func NewCmdPipelineLint() (*cobra.Command, *Options) {
	o := &Options{}

	cmd := &cobra.Command{
		Use:     "lint",
		Short:   "Lints the lighthouse trigger and tekton pipelines",
		Long:    cmdLong,
		Example: cmdExample,
		Aliases: []string{"kill"},
		Run: func(cmd *cobra.Command, args []string) {
			err := o.Run()
			helper.CheckErr(err)
		},
	}
	cmd.Flags().StringVarP(&o.Dir, "dir", "d", ".", "The directory to look for the .lighthouse folder")
	cmd.Flags().BoolVarP(&o.Recursive, "recursive", "r", false, "Recurisvely find all '.lighthouse' folders such as if linting a Pipeline Catalog")

	o.Options.AddFlags(cmd)

	return cmd, o
}

// Run implements this command
func (o *Options) Run() error {
	err := o.Options.Validate()
	if err != nil {
		return errors.Wrapf(err, "failed to validate options")
	}

	if o.Recursive {
		err := filepath.Walk(o.Dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info == nil || !info.IsDir() || info.Name() != ".lighthouse" {
				return nil
			}
			return o.LintDir(path)
		})
		if err != nil {
			return err
		}
	} else {
		dir := filepath.Join(o.Dir, ".lighthouse")
		err := o.LintDir(dir)
		if err != nil {
			return err
		}
	}

	return o.LogResults()
}

func (o *Options) LintDir(dir string) error {
	fs, err := ioutil.ReadDir(dir)
	if err != nil {
		return errors.Wrapf(err, "failed to read dir %s", dir)
	}
	for _, f := range fs {
		name := f.Name()
		if !f.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}

		triggerDir := filepath.Join(dir, name)
		triggersFile := filepath.Join(triggerDir, "triggers.yaml")
		exists, err := files.FileExists(triggersFile)
		if err != nil {
			return errors.Wrapf(err, "failed to check if file exists %s", triggersFile)
		}
		if !exists {
			continue
		}

		test := &linter.Test{
			File: triggersFile,
		}
		o.Tests = append(o.Tests, test)
		triggers := &triggerconfig.Config{}
		err = yamls.LoadFile(triggersFile, triggers)
		if err != nil {
			test.Error = err
			continue
		}

		o.loadConfigFile(triggers, triggerDir)
	}
	return nil
}

func (o *Options) loadConfigFile(repoConfig *triggerconfig.Config, dir string) *triggerconfig.Config {
	ctx := o.GetContext()
	for i := range repoConfig.Spec.Presubmits {
		r := &repoConfig.Spec.Presubmits[i]
		if r.SourcePath != "" {
			path := filepath.Join(dir, r.SourcePath)
			test := &linter.Test{
				File: path,
			}
			o.Tests = append(o.Tests, test)
			err := loadJobBaseFromSourcePath(ctx, path)
			if err != nil {
				test.Error = err
			}
		}
		if r.Agent == "" && r.PipelineRunSpec != nil {
			r.Agent = job.TektonPipelineAgent
		}
	}
	for i := range repoConfig.Spec.Postsubmits {
		r := &repoConfig.Spec.Postsubmits[i]
		if r.SourcePath != "" {
			path := filepath.Join(dir, r.SourcePath)
			test := &linter.Test{
				File: path,
			}
			o.Tests = append(o.Tests, test)
			err := loadJobBaseFromSourcePath(ctx, path)
			if err != nil {
				test.Error = err
			}
		}
		if r.Agent == "" && r.PipelineRunSpec != nil {
			r.Agent = job.TektonPipelineAgent
		}
	}
	return repoConfig
}

func loadJobBaseFromSourcePath(ctx context.Context, path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.Wrapf(err, "failed to load file %s", path)
	}
	if len(data) == 0 {
		return errors.Errorf("empty file file %s", path)
	}

	dir := filepath.Dir(path)
	message := fmt.Sprintf("file %s", path)

	getData := func(path string) ([]byte, error) {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read file %s", path)
		}
		return data, nil
	}

	pr, err := inrepo.LoadTektonResourceAsPipelineRun(data, dir, message, getData, nil)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal YAML file %s", path)
	}

	fieldError := pr.Validate(ctx)
	if fieldError != nil {
		return errors.Wrapf(fieldError, "failed to validate YAML file %s", path)
	}
	return nil
}
