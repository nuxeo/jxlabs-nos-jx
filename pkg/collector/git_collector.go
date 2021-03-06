package collector

import (
	"fmt"
	"io"
	"io/ioutil"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenkins-x/jx-logging/pkg/log"
	"github.com/jenkins-x/jx/v2/pkg/gits"
	"github.com/jenkins-x/jx/v2/pkg/util"
	"github.com/pkg/errors"
)

// GitCollector stores the state for the git collector
type GitCollector struct {
	gitInfo   *gits.GitRepository
	gitter    gits.Gitter
	gitBranch string
	gitKind   string
}

// NewGitCollector creates a new git based collector
func NewGitCollector(gitter gits.Gitter, gitURL string, gitBranch string, gitKind string) (Collector, error) {
	gitInfo, err := gits.ParseGitURL(gitURL)
	if err != nil {
		return nil, err
	}

	return &GitCollector{
		gitter:    gitter,
		gitInfo:   gitInfo,
		gitBranch: gitBranch,
		gitKind:   gitKind,
	}, nil
}

// CollectFiles collects files and returns the URLs
func (c *GitCollector) CollectFiles(patterns []string, outputPath string, basedir string) ([]string, error) {
	urls := []string{}

	gitClient := c.gitter
	storageGitInfo := c.gitInfo
	storageOrg := storageGitInfo.Organisation
	storageRepoName := storageGitInfo.Name

	ghPagesDir, err := cloneGitHubPagesBranchToTempDir(c.gitInfo.URL, gitClient, c.gitBranch)
	if err != nil {
		return urls, err
	}

	repoDir := filepath.Join(ghPagesDir, outputPath)
	err = os.MkdirAll(repoDir, 0755)
	if err != nil {
		return urls, err
	}

	for _, p := range patterns {
		fn := func(name string) error {
			var err error

			toName := name
			if basedir != "" {
				toName, err = filepath.Rel(basedir, name)
				if err != nil {
					return errors.Wrapf(err, "failed to remove basedir %s from %s", basedir, name)
				}
			}
			toFile := filepath.Join(repoDir, toName)
			toDir, _ := filepath.Split(toFile)
			err = os.MkdirAll(toDir, util.DefaultWritePermissions)
			if err != nil {
				return errors.Wrapf(err, "failed to create directory file %s", toDir)
			}
			err = util.CopyFileOrDir(name, toFile, true)
			if err != nil {
				return errors.Wrapf(err, "failed to copy file %s to %s", name, toFile)
			}

			rPath := strings.TrimPrefix(strings.TrimPrefix(toFile, ghPagesDir), "/")
			if rPath != "" {
				url := c.generateURL(storageOrg, storageRepoName, rPath)
				urls = append(urls, url)
			}
			return nil
		}

		err := util.GlobAllFiles("", p, fn)
		if err != nil {
			return urls, err
		}
	}

	err = gitClient.Add(ghPagesDir, repoDir)
	if err != nil {
		return urls, err
	}
	changes, err := gitClient.HasChanges(ghPagesDir)
	if err != nil {
		return urls, err
	}
	if !changes {
		return urls, nil
	}
	err = gitClient.CommitDir(ghPagesDir, fmt.Sprintf("Publishing files for path %s", outputPath))
	if err != nil {
		log.Logger().Errorf("%s", err)
		return urls, err
	}
	err = gitClient.Push(ghPagesDir, "origin", false, c.gitBranch)
	return urls, err
}

// CollectData collects the data storing it at the given output path and returning the URL
// to access it
func (c *GitCollector) CollectData(data io.Reader, outputPath string) (string, error) {
	u := ""
	gitClient := c.gitter
	storageGitInfo := c.gitInfo
	storageOrg := storageGitInfo.Organisation
	storageRepoName := storageGitInfo.Name

	ghPagesDir, err := cloneGitHubPagesBranchToTempDir(c.gitInfo.URL, gitClient, c.gitBranch)
	if err != nil {
		return u, err
	}

	defer os.RemoveAll(ghPagesDir)

	toFile := filepath.Join(ghPagesDir, outputPath)
	toDir, _ := filepath.Split(toFile)
	err = os.MkdirAll(toDir, util.DefaultWritePermissions)
	if err != nil {
		return u, errors.Wrapf(err, "failed to create directory file %s", toDir)
	}
	err = writeFile(toFile, data)
	if err != nil {
		return u, errors.Wrapf(err, "write file %s", toFile)
	}

	u = c.generateURL(storageOrg, storageRepoName, outputPath)

	err = gitClient.Add(ghPagesDir, toDir)
	if err != nil {
		return u, err
	}
	changes, err := gitClient.HasChanges(ghPagesDir)
	if err != nil {
		return u, err
	}
	if !changes {
		return u, nil
	}
	err = gitClient.CommitDir(ghPagesDir, fmt.Sprintf("Publishing files for path %s", outputPath))
	if err != nil {
		return u, err
	}
	err = gitClient.Push(ghPagesDir, "origin", false, "HEAD:"+c.gitBranch)
	return u, err
}

func writeFile(filePath string, data io.Reader) (err error) {
	dest, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, util.DefaultWritePermissions)
	if err != nil {
		return
	}
	defer func() {
		if e := dest.Close(); e != nil && err == nil {
			err = e
		}
	}()
	_, err = io.Copy(dest, data)
	return
}

func (c *GitCollector) generateURL(storageOrg string, storageRepoName string, rPath string) (url string) {
	if !c.gitInfo.IsGitHub() && c.gitKind == gits.KindGitHub {
		url = fmt.Sprintf("https://%s/raw/%s/%s/%s/%s", c.gitInfo.Host, storageOrg, storageRepoName, c.gitBranch, rPath)
	} else {
		switch c.gitKind {
		case gits.KindGitlab:
			url = fmt.Sprintf("https://%s/api/v4/projects/%s/repository/files/%s/raw?ref=%s",
				c.gitInfo.Host,
				neturl.PathEscape(fmt.Sprintf("%s/%s", storageOrg, storageRepoName)),
				neturl.PathEscape(rPath),
				neturl.QueryEscape(c.gitBranch))
		case gits.KindBitBucketServer:
			url = fmt.Sprintf("https://%s/projects/%s/repos/%s/raw/%s?at=%s", c.gitInfo.Host, storageOrg, storageRepoName, rPath, neturl.QueryEscape(fmt.Sprintf("refs/heads/%s", c.gitBranch)))
		default:
			// Fall back on GitHub for now
			url = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", storageOrg, storageRepoName, c.gitBranch, rPath)
		}
	}
	log.Logger().Infof("Publishing %s", util.ColorInfo(url))
	return url
}

// cloneGitHubPagesBranchToTempDir clones the github pages branch to a temp dir
func cloneGitHubPagesBranchToTempDir(sourceURL string, gitClient gits.Gitter, branchName string) (string, error) {
	// First clone the git repo
	ghPagesDir, err := ioutil.TempDir("", "jenkins-x-collect")
	if err != nil {
		return ghPagesDir, err
	}

	log.Logger().Infof("shallow cloning %s branch %s", sourceURL, branchName)
	err = gitClient.ShallowCloneBranch(sourceURL, branchName, ghPagesDir)
	if err != nil {
		log.Logger().Warnf("failed to shallow clone %s branch %s due to: %s", sourceURL, branchName, err.Error())

		os.RemoveAll(ghPagesDir)
		ghPagesDir, err = ioutil.TempDir("", "jenkins-x-collect")
		if err != nil {
			return ghPagesDir, err
		}
		log.Logger().Infof("error doing shallow clone of branch %s: %v", branchName, err)
		// swallow the error
		log.Logger().Infof("No existing %s branch so creating it", branchName)
		// branch doesn't exist, so we create it following the process on https://help.github.com/articles/creating-project-pages-using-the-command-line/
		err = gitClient.Clone(sourceURL, ghPagesDir)
		if err != nil {
			return ghPagesDir, err
		}
		err = gitClient.CheckoutOrphan(ghPagesDir, branchName)
		if err != nil {
			return ghPagesDir, err
		}
		err = gitClient.RemoveForce(ghPagesDir, ".")
		if err != nil {
			return ghPagesDir, err
		}
		err = os.Remove(filepath.Join(ghPagesDir, ".gitignore"))
		if err != nil {
			// Swallow the error, doesn't matter
		}
	}
	return ghPagesDir, nil
}
