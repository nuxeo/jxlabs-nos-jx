// +build integration

package helmfile

import (
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jenkins-x/jx/pkg/cmd/testhelpers"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	v1 "k8s.io/api/core/v1"

	mocks "github.com/jenkins-x/jx/v2/pkg/cmd/clients/mocks"
	helmfile2 "github.com/jenkins-x/jx/v2/pkg/helmfile"
	kube_test "github.com/jenkins-x/jx/v2/pkg/kube/mocks"
	. "github.com/petergtz/pegomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jenkins-x/jx/v2/pkg/cmd/create/options"
	"github.com/jenkins-x/jx/v2/pkg/cmd/opts"
	helm_test "github.com/jenkins-x/jx/v2/pkg/helm/mocks"

	"github.com/jenkins-x/jx/v2/pkg/util"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func TestGeneratedHelmfiles(t *testing.T) {
	rootTempDir, err := ioutil.TempDir("", "test-applications-config")
	assert.NoError(t, err, "should create a temporary config Dir")

	for _, name := range []string{"force", "istio", "dedupe_repositories", "empty-system", "empty", "alias", "namespace-chart", "override-values"} {
		tempDir := filepath.Join(rootTempDir, name)
		sourceDir := filepath.Join("test_data", name)
		o := &CreateHelmfileOptions{
			outputDir:     tempDir,
			Dir:           sourceDir,
			CreateOptions: *getCreateOptions(),
		}
		o.SetEnvironmentContext(createTestEnvironmentContext(t))
		err = o.Run()
		assert.NoError(t, err, "failed to generate helmfiles for %s", name)

		for _, folder := range []string{"apps", "system"} {
			_, got, err := loadHelmfile(path.Join(tempDir, folder))
			assert.NoError(t, err)

			_, want, err := loadHelmfile(path.Join(sourceDir, "expected", folder))
			assert.NoError(t, err)

			if diff := cmp.Diff(strings.TrimSpace(got), strings.TrimSpace(want)); diff != "" {
				t.Errorf("Unexpected helmfile generated for %s folder %s", name, folder)
				t.Log(diff)

				t.Logf("generated helmfile for %s in %s:\n", name, folder)
				t.Logf("\n%s\n", got)
			}
		}
	}
}

func TestExtraAppValues(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test-applications-config")
	assert.NoError(t, err, "should create a temporary config Dir")

	o := &CreateHelmfileOptions{
		outputDir:     tempDir,
		Dir:           path.Join("test_data", "extra-values"),
		CreateOptions: *getCreateOptions(),
	}
	o.SetEnvironmentContext(createTestEnvironmentContext(t))
	err = o.Run()
	assert.NoError(t, err)

	h, _, err := loadHelmfile(path.Join(tempDir, "system"))
	assert.NoError(t, err)

	// assert we added the local values.yaml for the velero app
	assert.Equal(t, "velero/values.yaml", h.Releases[0].Values[2])
}

func TestNoLocalHelmRepoMatch(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test-applications-config")
	assert.NoError(t, err, "should create a temporary config Dir")

	o := &CreateHelmfileOptions{
		outputDir:     tempDir,
		Dir:           path.Join("test_data", "no-local-helm-repo"),
		CreateOptions: *getCreateOptions(),
	}
	o.SetEnvironmentContext(createTestEnvironmentContext(t))
	err = o.Run()
	assert.NoError(t, err)

	h, _, err := loadHelmfile(path.Join(tempDir, "apps"))
	assert.NoError(t, err)

	chartName := strings.Split(h.Releases[0].Chart, "/")
	assert.Equal(t, 2, len(chartName))
	assert.Equal(t, chartName[0], h.Repositories[0].Name)
}

func TestLocalHelmRepoMatch(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test-applications-config")
	assert.NoError(t, err, "should create a temporary config Dir")

	o := &CreateHelmfileOptions{
		outputDir:     tempDir,
		Dir:           path.Join("test_data", "matching-local-helm-repo"),
		CreateOptions: *getCreateOptionsWithLocalHelmRepo(),
	}
	o.SetEnvironmentContext(createTestEnvironmentContext(t))
	err = o.Run()
	assert.NoError(t, err)

	h, _, err := loadHelmfile(path.Join(tempDir, "apps"))
	assert.NoError(t, err)

	chartName := strings.Split(h.Releases[0].Chart, "/")
	assert.Equal(t, 2, len(chartName))
	assert.Equal(t, "cheese", chartName[0])
}

func TestCreateNamespaceChart(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test-applications-config")
	assert.NoError(t, err, "should create a temporary config Dir")

	o := &CreateHelmfileOptions{
		outputDir:  tempDir,
		Dir:        path.Join("test_data", "create-namespace-chart"),
		valueFiles: []string{"foo/bar.yaml"},
	}
	configureTestCommonOptions(t, o)
	o.SetEnvironmentContext(createTestEnvironmentContext(t))
	err = o.Run()
	assert.NoError(t, err)

	h, _, err := loadHelmfile(path.Join(tempDir, "system"))
	assert.NoError(t, err)

	exists, err := util.FileExists(path.Join(tempDir, "system", "generated", "foo", "values.yaml"))
	assert.True(t, exists, "generated namespace values file not found")

	// assert we added the values file passed in as a CLI flag
	assert.Equal(t, 2, len(h.Releases), "should have two charts, one for the app and a second added to create the missing namespace")

	for _, release := range h.Releases {
		if release.Name == "velero" {
			assert.Equal(t, "foo/bar.yaml", release.Values[1])
		} else {
			assert.Equal(t, "namespace-foo", release.Name)
			assert.Equal(t, path.Join("generated", "foo", "values.yaml"), release.Values[0])
		}
	}

}

func loadHelmfile(dir string) (*helmfile2.HelmState, string, error) {
	text := ""
	fileName := helmfile
	if dir != "" {
		fileName = filepath.Join(dir, helmfile)
	}

	exists, err := util.FileExists(fileName)
	if err != nil || !exists {
		return nil, text, errors.Errorf("no %s found in directory %s", fileName, dir)
	}

	config := &helmfile2.HelmState{}

	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		return config, text, fmt.Errorf("failed to load file %s due to %s", fileName, err)
	}
	text = string(data)
	validationErrors, err := util.ValidateYaml(config, data)
	if err != nil {
		return config, text, fmt.Errorf("failed to validate YAML file %s due to %s", fileName, err)
	}

	if len(validationErrors) > 0 {
		return config, text, fmt.Errorf("Validation failures in YAML file %s:\n%s", fileName, strings.Join(validationErrors, "\n"))
	}
	err = yaml.Unmarshal(data, config)
	if err != nil {
		return config, text, fmt.Errorf("Failed to unmarshal YAML file %s due to %s", fileName, err)
	}

	return config, text, err
}

func getCreateOptions() *options.CreateOptions {
	// default jx namespace
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "jx",
		},
	}
	// mock factory
	factory := mocks.NewMockFactory()
	// mock Kubernetes interface
	kubeInterface := fake.NewSimpleClientset(ns)

	// Override CreateKubeClient to return mock Kubernetes interface
	When(factory.CreateKubeClient()).ThenReturn(kubeInterface, "jx", nil)
	commonOpts := opts.NewCommonOptionsWithFactory(factory)

	helmer := helm_test.NewMockHelmer()
	kuber := kube_test.NewMockKuber()

	commonOpts.SetHelm(helmer)
	commonOpts.SetKube(kuber)

	return &options.CreateOptions{
		CommonOptions: &commonOpts,
	}
}

func getCreateOptionsWithLocalHelmRepo() *options.CreateOptions {
	// default jx namespace
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "jx",
		},
	}
	// mock factory
	factory := mocks.NewMockFactory()
	// mock Kubernetes interface
	kubeInterface := fake.NewSimpleClientset(ns)

	// Override CreateKubeClient to return mock Kubernetes interface
	When(factory.CreateKubeClient()).ThenReturn(kubeInterface, "jx", nil)
	commonOpts := opts.NewCommonOptionsWithFactory(factory)

	helmer := helm_test.NewMockHelmer()
	repos := make(map[string]string)
	repos["cheese"] = "gs://bar/charts"

	When(helmer.ListRepos()).ThenReturn(repos, nil)

	kuber := kube_test.NewMockKuber()

	commonOpts.SetHelm(helmer)
	commonOpts.SetKube(kuber)

	return &options.CreateOptions{
		CommonOptions: &commonOpts,
	}
}

func configureTestCommonOptions(t *testing.T, o *CreateHelmfileOptions) {
	co := &opts.CommonOptions{}
	testhelpers.ConfigureTestOptions(co, gits.NewGitCLI(), helm.NewHelmCLI("helm", helm.V3, "", true))
	o.CommonOptions = co
}

func createTestEnvironmentContext(t *testing.T) *envctx.EnvironmentContext {
	versionsDir := path.Join("test_data", "jenkins-x-versions")
	assert.DirExists(t, versionsDir)

	return &envctx.EnvironmentContext{
		VersionResolver: &versionstream.VersionResolver{
			VersionsDir: versionsDir,
		},
	}
}
