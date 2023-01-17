package operators

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/openshift/osde2e/pkg/common/alert"
	"github.com/openshift/osde2e/pkg/common/concurrentviper"
	"github.com/openshift/osde2e/pkg/common/config"
	"github.com/openshift/osde2e/pkg/common/helper"
	"github.com/openshift/osde2e/pkg/common/label"
)

var configureAlertManagerOperators string = "[Suite: operators] [OSD] Configure AlertManager Operator"

func init() {
	alert.RegisterGinkgoAlert(configureAlertManagerOperators, "SD-SREP", "@sd-srep-team-thor", "sd-cicd-alerts", "sd-cicd@redhat.com", 4)
}

var _ = ginkgo.Describe(configureAlertManagerOperators, label.Operators, func() {
	ginkgo.BeforeEach(func() {
		if concurrentviper.GetBool(config.Hypershift) {
			ginkgo.Skip("Configure AlertManager Operator is not supported on HyperShift")
		}
	})

	operatorName := "configure-alertmanager-operator"
	var operatorNamespace string = "openshift-monitoring"
	var operatorLockFile string = "configure-alertmanager-operator-lock"
	var defaultDesiredReplicas int32 = 1

	clusterRoles := []string{
		"configure-alertmanager-operator",
	}

	clusterRoleBindings := []string{}

	serviceAccounts := []string{
		"configure-alertmanager-operator",
	}

	h := helper.New()
	checkClusterServiceVersion(h, operatorNamespace, operatorName)
	checkConfigMapLockfile(h, operatorNamespace, operatorLockFile)
	checkDeployment(h, operatorNamespace, operatorName, defaultDesiredReplicas)
	checkClusterRoles(h, clusterRoles, true)
	checkClusterRoleBindings(h, clusterRoleBindings, true)
	checkServiceAccounts(h, operatorNamespace, serviceAccounts)
	checkRolesWithNamePrefix(h, operatorNamespace, operatorName, 2)
	checkRoleBindingsWithNamePrefix(h, operatorNamespace, operatorName, 2)
	checkUpgrade(helper.New(), "openshift-monitoring", "configure-alertmanager-operator",
		"configure-alertmanager-operator", "configure-alertmanager-operator-registry")
})
