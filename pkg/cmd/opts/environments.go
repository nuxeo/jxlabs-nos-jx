package opts

import (
	jenkinsv1 "github.com/jenkins-x/jx-api/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx-logging/pkg/log"
	"github.com/jenkins-x/jx/v2/pkg/kube"
	"github.com/jenkins-x/jx/v2/pkg/kube/services"
)

// RegisterEnvironmentCRD registers the CRD for environmnt
func (o *CommonOptions) RegisterEnvironmentCRD() error {
	apisClient, err := o.ApiExtensionsClient()
	if err != nil {
		return err
	}
	err = kube.RegisterEnvironmentCRD(apisClient)
	return err
}

// GetDevEnv gets the Development Environment CRD as devEnv,
// and also tells the user whether the development environment is using gitOps
func (o *CommonOptions) GetDevEnv() (gitOps bool, devEnv *jenkinsv1.Environment) {
	// We're going to need to know whether the team is using GitOps for the dev env or not,
	// and also access the team settings, so load those
	jxClient, ns, err := o.JXClientAndDevNamespace()
	if err != nil {
		log.Logger().Warnf("when attempting to create jx client. %v", err)
		return false, &jenkinsv1.Environment{}
	} else {
		devEnv, err := kube.GetDevEnvironment(jxClient, ns)
		if err != nil {
			log.Logger().Warnf("when attempting to load team settings. %v", err)
			return false, &jenkinsv1.Environment{}
		}
		gitOps := false
		if devEnv == nil {
			devEnv = &jenkinsv1.Environment{}
			devEnv.Spec.Namespace = ns
		}
		if devEnv.Spec.Source.URL != "" {
			gitOps = true
		}
		return gitOps, devEnv
	}
}

// ResolveChartMuseumURL resolves the current Chart Museum URL so we can pass it into a remote Environment's
// git repository
func (o *CommonOptions) ResolveChartMuseumURL() (string, error) {
	kubeClient, ns, err := o.KubeClientAndDevNamespace()
	if err != nil {
		return "", errors.Wrap(err, "creating Kiube client")
	}
	answer, err := services.FindServiceURL(kubeClient, ns, kube.ServiceChartMuseum)
	if err != nil && apierrors.IsNotFound(err) {
		err = nil
	}
	if err != nil || answer == "" {
		// lets try find a `chartmusem` ingress
		var err2 error
		answer, err2 = services.FindIngressURL(kubeClient, ns, "chartmuseum")
		if err2 != nil && apierrors.IsNotFound(err2) {
			err2 = nil
		}
		if err2 == nil && answer != "" {
			return answer, nil
		}
	}
	if answer == "" {
		jxClient, ns, err := o.JXClientAndDevNamespace()
		if err != nil {
			return "", errors.Wrap(err, "creating JX client")
		}
		env, err := kube.GetDevEnvironment(jxClient, ns)
		if err != nil && apierrors.IsNotFound(err) {
			err = nil
		}
		if env != nil {
			requirements, err := config.GetRequirementsConfigFromTeamSettings(&env.Spec.TeamSettings)
			if err != nil {
				return answer, errors.Wrapf(err, "getting requirements from dev Environment")
			}
			if requirements != nil {
				if requirements.Cluster.ChartRepository != "" {
					return requirements.Cluster.ChartRepository, nil
				}
			}
		}
	}
	return answer, err
}
