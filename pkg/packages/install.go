package packages

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/pkg/errors"

	"github.com/jenkins-x/jx-logging/pkg/log"

	"github.com/google/uuid"
	"github.com/jenkins-x/jx/v2/pkg/util"
	"github.com/jenkins-x/jx/v2/pkg/versionstream"
)

// InstallOrUpdateBinary installs or updates a binary
func InstallOrUpdateBinary(options InstallOrUpdateBinaryOptions) error {
	isInstalled, err := IsBinaryWithProperVersionInstalled(options.Binary, options.Version, options.VersionExtractor)
	if err != nil {
		return err
	}
	if isInstalled {
		return nil
	}

	downloadUrlTemplate := options.DownloadUrlTemplate
	if !options.Archived {
		downloadUrlTemplate = BinaryWithExtension(downloadUrlTemplate)
	}
	urlTemplate, err := template.New(options.Binary).Parse(downloadUrlTemplate)
	if err != nil {
		return err
	}
	binDir, err := util.JXBinLocation()
	if err != nil {
		return err
	}
	fileName := options.Binary
	if !options.SkipPathScan {
		flag, err := ShouldInstallBinary(options.Binary)
		fileName = options.Binary
		if err != nil || !flag {
			return err
		}
	}

	if options.Version == "" {
		configDir, err := util.ConfigDir()
		if err != nil {
			return err
		}
		versionFile := filepath.Join(configDir, "jenkins-x-versions", "packages", options.Binary+".yml")
		ver, err := versionstream.LoadStableVersionFile(versionFile)
		if err != nil {
			return err
		}
		if ver.Version != "" {
			options.Version = ver.Version
		}
	}

	if options.Version == "" {
		options.Version, err = util.GetLatestVersionStringFromGitHub(options.GitHubOrganization, options.Binary)
		if err != nil {
			return err
		}
	}
	extension := "tar.gz"
	if runtime.GOOS == "windows" {
		extension = "zip"
	}
	clientUrlBuffer := bytes.NewBufferString("")
	variables := map[string]string{"version": options.Version, "os": runtime.GOOS, "osTitle": strings.Title(runtime.GOOS), "arch": runtime.GOARCH, "extension": extension}
	err = urlTemplate.Execute(clientUrlBuffer, variables)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(binDir, fileName)
	tarFile := fullPath
	if options.Archived {
		tarFile = tarFile + "." + extension
	}
	downloadUrl := clientUrlBuffer.String()
	if options.DownloadUrlTemplateLowerCase {
		downloadUrl = strings.ToLower(downloadUrl)
	}
	err = DownloadFile(downloadUrl, tarFile)
	if err != nil {
		return err
	}
	fileNameInArchive := fileName
	if options.ArchiveDirectory != "" {
		fileNameInArchive = filepath.Join(options.ArchiveDirectory, fileName)
	}
	if options.Archived {
		if extension == "zip" {
			zipDir := filepath.Join(binDir, options.Binary+"-tmp-"+uuid.New().String())
			err = os.MkdirAll(zipDir, util.DefaultWritePermissions)
			if err != nil {
				return err
			}
			err = util.Unzip(tarFile, zipDir)
			if err != nil {
				return err
			}

			f := filepath.Join(zipDir, fileNameInArchive)
			exists, err := util.FileExists(f)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("Could not find file %s inside the downloaded file!", f)
			}
			err = os.Rename(f, fullPath)
			if err != nil {
				return err
			}
			err = os.RemoveAll(zipDir)
		} else {
			err = util.UnTargz(tarFile, binDir, []string{options.Binary, fileNameInArchive})
		}
		if err != nil {
			return err
		}
		err = os.Remove(tarFile)
		if err != nil {
			return err
		}
	}

	err = RememberInstalledPackage(options.Binary, options.Version)
	if err != nil {
		return err
	}

	return os.Chmod(fullPath, 0755)
}

// AddRequiredBinary add the required binary
func AddRequiredBinary(binName string, deps []string) []string {
	d := BinaryShouldBeInstalled(binName)
	if d != "" && util.StringArrayIndex(deps, d) < 0 {
		deps = append(deps, d)
	}
	return deps
}

// FilterInstalledDependencies filters installed dependencies
func FilterInstalledDependencies(deps []string) []string {
	depsToInstall := []string{}
	for _, d := range deps {
		binary := BinaryShouldBeInstalled(d)
		if binary != "" {
			depsToInstall = append(depsToInstall, binary)
		}
	}
	return depsToInstall
}

// InstallOrUpdateBinaryOptions options for install or update binary
type InstallOrUpdateBinaryOptions struct {
	Binary                       string
	GitHubOrganization           string
	DownloadUrlTemplate          string
	DownloadUrlTemplateLowerCase bool
	Version                      string
	SkipPathScan                 bool
	VersionExtractor             VersionExtractor
	Archived                     bool
	ArchiveDirectory             string
}

// ShouldInstallBinary checks if the given binary should be installed
func ShouldInstallBinary(name string) (bool, error) {
	fileName := BinaryWithExtension(name)
	download := false

	binDir, err := util.JXBinLocation()
	if err != nil {
		return download, errors.Wrapf(err, "unable to find JXBinLocation at %s", binDir)
	}

	if util.Contains(GlobalBinaryPathAllowlist, name) {
		_, err = exec.LookPath(fileName)
		if err != nil {
			log.Logger().Warnf("%s is not available on your PATH", util.ColorInfo(fileName))
			return true, nil
		}
		return false, nil
	}

	exists, err := util.FileExists(filepath.Join(binDir, fileName))
	if exists {
		log.Logger().Debugf("%s is already available in your JXBIN at %s", util.ColorInfo(fileName), util.ColorInfo(binDir))
		return download, nil
	}
	if err != nil {
		return download, errors.Wrapf(err, "unable to check files on %s", binDir)
	}

	download = true
	return download, nil
}

// BinaryShouldBeInstalled appends the binary to the deps array if it cannot be found on the $PATH
func BinaryShouldBeInstalled(d string) string {
	shouldInstall, err := ShouldInstallBinary(d)
	if err != nil {
		log.Logger().Warnf("Error detecting if binary should be installed: %s", err.Error())
		return ""
	}
	if shouldInstall {
		return d
	}
	return ""
}

// InstallKubectlWithVersion install a specific version of kubectl
func InstallKubectlWithVersion(version string, skipPathScan bool) error {
	return InstallOrUpdateBinary(InstallOrUpdateBinaryOptions{
		Binary:                       "kubectl",
		GitHubOrganization:           "",
		DownloadUrlTemplate:          "https://storage.googleapis.com/kubernetes-release/release/v{{.version}}/bin/{{.osTitle}}/{{.arch}}/kubectl",
		DownloadUrlTemplateLowerCase: true,
		Version:                      version,
		SkipPathScan:                 skipPathScan,
		VersionExtractor:             nil,
		Archived:                     false,
	})
}

// InstallKubectl installs kubectl
func InstallKubectl(skipPathScan bool) error {
	return InstallKubectlWithVersion(KubectlVersion, skipPathScan)
}

// UninstallBinary uninstalls given binary
func UninstallBinary(binDir string, name string) error {
	fileName := name
	if runtime.GOOS == "windows" {
		fileName += ".exe"
	}
	// try to remove the binary from all paths
	var err error
	for {
		path, err := exec.LookPath(fileName)
		if err == nil {
			err := os.Remove(path)
			if err != nil {
				return err
			}
		} else {
			break
		}
	}
	path := filepath.Join(binDir, fileName)
	exists, err := util.FileExists(path)
	if err != nil {
		return nil
	}
	if exists {
		return os.Remove(path)
	}
	return nil
}
