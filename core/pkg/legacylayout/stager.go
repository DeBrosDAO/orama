package legacylayout

import "github.com/DeBrosOfficial/network/pkg/privhelper"

// PrivHelperStager stages through orama-privhelper, which validates every name
// again and writes with O_NOFOLLOW into trees only root can write.
type PrivHelperStager struct{}

func (PrivHelperStager) SetUnitEnv(namespace, service, contents string) error {
	return privhelper.SetUnitEnv(namespace, service, contents)
}

func (PrivHelperStager) SetDeploymentEnv(instance, contents string) error {
	return privhelper.SetDeploymentEnv(instance, contents)
}

func (PrivHelperStager) SetDeploymentToken(instance, token string) error {
	return privhelper.SetDeploymentToken(instance, token)
}
