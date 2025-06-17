package harness_runner

import (
	"context"
	"log"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/osde2e-common/pkg/clients/ocm"
	viper "github.com/openshift/osde2e/pkg/common/concurrentviper"
	"github.com/openshift/osde2e/pkg/common/config"
	"github.com/openshift/osde2e/pkg/common/label"
	"github.com/openshift/osde2e/pkg/common/load"
	"github.com/openshift/osde2e/pkg/common/providers/ocmprovider"
	"github.com/openshift/osde2e/pkg/e2e/executor"
)

var (
	HarnessEntries   []ginkgo.TableEntry
	timeoutInSeconds int
)

var _ = ginkgo.Describe("Test harness", ginkgo.Ordered, ginkgo.ContinueOnFailure, label.TestHarness, func() {
	harnesses := viper.GetStringSlice(config.Tests.TestHarnesses)
	if viper.IsSet(config.Tests.HarnessTimeout) {
		timeoutInSeconds = viper.GetInt(config.Tests.HarnessTimeout)
	} else {
		timeoutInSeconds = viper.GetInt(config.Tests.PollingTimeout)
	}
	for _, harness := range harnesses {
		HarnessEntries = append(HarnessEntries, ginkgo.Entry(harness+" should pass", harness))
	}

	ginkgo.BeforeAll(func(ctx context.Context) {
		log.Println("Harnesses to run: ", harnesses)
	})

	ginkgo.DescribeTable("execution",
		func(ctx context.Context, harness string) {
			log.Printf("======= RUNNING HARNESS: %s =======", harness)
			//suffix := "h-" + util.RandomStr(5)
			var ocmUrl ocm.Environment
			switch viper.GetString(ocmprovider.Env) {
			case "stage":
				ocmUrl = ocm.Stage
			case "int":
				ocmUrl = ocm.Integration
			default:
				ginkgo.Fail("Unexpected OCM_ENV - use 'stage' or 'int'")
			}
			passThruSecrets := map[string]string{}
			passThruSecrets = load.GetPassthruSecrets(passThruSecrets)
			execConfig := &executor.Config{
				Image:               harness,
				OutputDir:           "/test-run-results",
				Environment:         ocmUrl,
				ClusterID:           viper.GetString(config.Cluster.ID),
				CloudProviderID:     viper.GetString(config.CloudProvider.CloudProviderID),
				CloudProviderRegion: viper.GetString(config.CloudProvider.CloudProviderID),
				PassthruSecrets:     passThruSecrets,
				Timeout:             5 * time.Minute,
				KeepPods:            viper.GetBool(config.Cluster.SkipDestroyCluster),
				//Name:                "e2e-" + suffix,
			}
			ex, _ := executor.New(ginkgo.GinkgoLogr, execConfig)
			err := ex.Execute(ctx)
			Expect(err).NotTo(HaveOccurred(), "Could not execute ")
		},
		HarnessEntries)

	//	FIXME no S3 uploads, no junit downloads, no Addind specs into main ginkgo report for each harness run
})
