package pr

import (
	"os"
	"strings"

	"github.com/jenkins-x/jx/v2/pkg/cmd/clients"
	"github.com/jenkins-x/jx/v2/pkg/cmd/opts"
	"github.com/jenkins-x/jx/v2/pkg/gits"
	"github.com/jenkins-x/jx/v2/pkg/gits/operations"
	"github.com/jenkins-x/jx-logging/pkg/log"
	"github.com/pkg/errors"
)

// RegexFiles a struct to hold the configurations for a regular expression function
type RegexFiles struct {
	Regex string
	Files []string
}

// CreateRegexChangeFunctions creates a number of regular expression change functions
func CreateRegexChangeFunctions(version string, rs ...RegexFiles) ([]operations.ChangeFilesFn, error) {
	modifyFns := []operations.ChangeFilesFn{}
	for _, r := range rs {
		fn, err := operations.CreatePullRequestRegexFn(version, r.Regex, r.Files...)
		if err != nil {
			return modifyFns, err
		}
		log.Logger().Infof("adding modify function for regex: %s for files %s", r.Regex, strings.Join(r.Files, " "))
		modifyFns = append(modifyFns, fn)
	}
	return modifyFns, nil
}

// CreatePromotePR creates a pull request using the given change functions on the git URLs
func CreatePromotePR(o *StepCreatePrOptions, fn func(string) ([]operations.ChangeFilesFn, error), useOldVerisons bool, gitURLs ...string) error {
	if o == nil {
		o = &StepCreatePrOptions{}
	}
	if o.CommonOptions == nil {
		f := clients.NewFactory()
		o.CommonOptions = opts.NewCommonOptionsWithTerm(f, os.Stdin, os.Stdout, os.Stderr)
	}
	credBinary := os.Getenv("JX_CREDENTIAL_BINARY")
	if credBinary == "" {
		os.Setenv("JX_CREDENTIAL_BINARY", "jx")
	}
	o.GitURLs = gitURLs
	if o.Version == "" {
		o.Version = os.Getenv("VERSION")
	}
	if o.Base == "" {
		o.Base = "master"
	}
	if o.BranchName == "" {
		o.BranchName = "master"
	}
	if err := o.ValidateOptions(false); err != nil {
		return errors.WithStack(err)
	}
	modifyFns, err := fn(o.Version)
	if err != nil {
		return errors.Wrap(err, "failed to create modify functions")
	}
	log.Logger().Infof("promoting version %s", o.Version)

	err = o.CreatePullRequest("promote", func(dir string, gitInfo *gits.GitRepository) ([]string, error) {
		log.Logger().Infof("modifying source in directory %s", dir)
		var oldVersions []string
		for i, fn := range modifyFns {
			log.Logger().Infof("invoking modify function %d", i+1)
			answer, err := fn(dir, gitInfo)
			if err != nil {
				return nil, errors.WithStack(err)
			}
			if useOldVerisons {
				oldVersions = append(oldVersions, answer...)
			}
		}
		log.Logger().Infof("completed modify functions with old versions: %s", strings.Join(oldVersions, ", "))
		return oldVersions, nil

	})
	if err != nil {
		return errors.Wrap(err, "failed to create PullRequest")
	}
	log.Logger().Infof("promoted version %s", o.Version)
	return nil
}
