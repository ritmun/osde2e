// Package openshift runs the OpenShift extended test suite.
package openshift

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	viper "github.com/openshift/osde2e/pkg/common/concurrentviper"
	"github.com/openshift/osde2e/pkg/common/config"
	"github.com/openshift/osde2e/pkg/common/helper"
	"github.com/openshift/osde2e/pkg/common/label"
	"github.com/openshift/osde2e/pkg/common/runner"
	"github.com/openshift/osde2e/pkg/common/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DefaultE2EConfig is the base configuration for E2E runs.
var DefaultE2EConfig = E2EConfig{
	OutputDir: "/test-run-results",
	Tarball:   false,
	Suite:     "kubernetes/conformance",
	Flags: []string{
		"--include-success",
		"--junit-dir=" + runner.DefaultRunner.OutputDir,
	},
	ServiceAccountDir: "/var/run/secrets/kubernetes.io/serviceaccount",
}

var (
	conformanceK8sTestName       = "[Suite: conformance][k8s]"
	conformanceOpenshiftTestName = "[Suite: conformance][openshift]"
)

var _ = ginkgo.Describe(conformanceK8sTestName, func() {
	defer ginkgo.GinkgoRecover()
	h := helper.New()

	e2eTimeoutInSeconds := 7200
	ginkgo.It("should run until completion", func(ctx context.Context) {
		// configure tests
		h.SetServiceAccount(ctx, "system:serviceaccount:%s:cluster-admin")

		cfg := DefaultE2EConfig
		cmd := cfg.GenerateOcpTestCmdBlock()

		// setup runner
		r := h.Runner(cmd)

		r.Name = "k8s-conformance"

		// run tests
		stopCh := make(chan struct{})

		err := r.Run(e2eTimeoutInSeconds, stopCh)
		Expect(err).NotTo(HaveOccurred())

		// get results
		results, err := r.RetrieveTestResults()

		// write results
		h.WriteResults(results)

		// evaluate results
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = ginkgo.Describe(conformanceOpenshiftTestName, ginkgo.Ordered, label.OCPNightlyBlocking, func() {
	defer ginkgo.GinkgoRecover()
	h := helper.New()
	var k8s *openshift.Client

	ginkgo.BeforeAll(func(ctx context.Context) {
		log.SetLogger(ginkgo.GinkgoLogr)
		var err error
		k8s, err = openshift.NewFromKubeconfig(viper.GetString(config.Kubeconfig.Path), ginkgo.GinkgoLogr)
		Expect(err).ShouldNot(HaveOccurred(), "Unable to setup k8s client")
	})

	e2eTimeoutInSeconds := 7200
	ginkgo.It("should run until completion", func(ctx context.Context) {
		h.SetServiceAccount(ctx, "system:serviceaccount:%s:cluster-admin")
		// configure tests
		cfg := DefaultE2EConfig
		if viper.GetString(config.Tests.OCPTestSuite) != "" {
			cfg.Suite = "openshift/conformance/parallel " + viper.GetString(config.Tests.OCPTestSuite)
		} else {
			cfg.Suite = "openshift/conformance/parallel suite"
		}

		// setup runner
		r := h.RunnerWithNoCommand()
		suffix := util.RandomStr(5)
		r.Name = "osde2e-main-" + suffix
		latestImageStream, err := r.GetLatestImageStreamTag()
		Expect(err).NotTo(HaveOccurred(), "Could not get latest imagestream tag")

		// create test command configmap
		// testcmd := cfg.GenerateOcpTestCmdBlock()
		testcmd := "touch abc.xml && exit 0"
		testcfgData := make(map[string]string)
		testcfgData["test-cmd.sh"] = testcmd
		testcfgmap := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cmd-" + suffix,
				Namespace: h.CurrentProject(),
			},
			Data: testcfgData,
		}
		err = k8s.Create(ctx, testcfgmap)
		Expect(err).NotTo(HaveOccurred())

		// create push results command configmap
		pushcmd := getPushCmd("openshift-conformance", runner.DefaultRunner.OutputDir)
		pushcfgData := make(map[string]string)

		pushcfgData["push-results.sh"] = pushcmd
		pushcfgmap := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "push-results-" + suffix,
				Namespace: h.CurrentProject(),
			},
			Data: pushcfgData,
		}
		err = k8s.Create(ctx, pushcfgmap)
		Expect(err).NotTo(HaveOccurred())

		// create test job
		testjob := getTestJob(h.CurrentProject(),
			e2eTimeoutInSeconds,
			latestImageStream,
			suffix,
			"openshift-conformance",
			"cluster-admin",
		)
		err = k8s.Create(ctx, testjob)
		Expect(err).NotTo(HaveOccurred())

		r = h.SetRunnerCommand(getMainPodCmd("openshift-conformance", runner.DefaultRunner.OutputDir), r)

		// run collector pod
		stopCh := make(chan struct{})
		err = r.Run(e2eTimeoutInSeconds, stopCh)
		Expect(err).NotTo(HaveOccurred())

		// get results - also returns error if no xml file found
		// keeps tests from showing green if they didn't produce xml output.
		results, err := r.RetrieveTestResults()

		// write results, including non-xml log files
		h.WriteResults(results)

		Expect(err).NotTo(HaveOccurred(), "Error reading xml results, test may have exited abruptly. Check conformance logs for errors")
	})
})

func getMainPodCmd(jobname string, outdir string) string {
	return `set +e

while oc get job/` + jobname + ` -o=jsonpath='{.status}' | grep -q active; do sleep 1; done

mkdir -p "` + outdir + `/containerLogs"
JOB_POD=$(oc get pods -l job-name=` + jobname + ` -o=jsonpath='{.items[0].metadata.name}')

if [[ ! $JOB_POD ]]; then
  echo "test harness pod not found, may have been terminated. exiting"

else
  echo "found test harness pod $JOB_POD"
  oc logs $JOB_POD -c test-harness > "` + outdir + `/containerLogs/${JOB_POD}-test-harness.log"
  oc logs $JOB_POD -c push-results > "` + outdir + `/containerLogs/${JOB_POD}-push-results.log"
fi`
}

func getTestJob(namespace string, timeout int, image string, suffix string, podname string, saname string) *batchv1.Job {
	to := int64(timeout)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podname,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ActiveDeadlineSeconds: &to,
					Containers: []corev1.Container{
						{
							Name:    "test-harness",
							Image:   image,
							Command: []string{"/bin/sh", "/test-cmd/test-cmd.sh"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "test-output", MountPath: runner.DefaultRunner.OutputDir},
								{Name: "test-cmd", MountPath: "/test-cmd"},
							},
						},
						{
							Name:    "push-results",
							Image:   image,
							Command: []string{"/bin/sh", "/push-results/push-results.sh"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "test-output", MountPath: runner.DefaultRunner.OutputDir},
								{Name: "push-results", MountPath: "/push-results"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name:         "test-output",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name: "push-results",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "push-results-" + suffix,
									},
									DefaultMode: pointer.Int32(0o755),
								},
							},
						},
						{
							Name: "test-cmd",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "test-cmd-" + suffix,
									},
									DefaultMode: pointer.Int32(0o755),
								},
							},
						},
					},
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: saname,
				},
			},
		},
	}
}

func getPushCmd(jobname string, outdir string) string {
	return `
#!/usr/bin/env bash

JOB_POD=$(oc get pods -l job-name=` + jobname + ` -o=jsonpath='{.items[0].metadata.name}')
echo "Found Job Pod: $JOB_POD"
while ! oc get pod $JOB_POD -o jsonpath='{.status.containerStatuses[?(@.name=="test-harness")].state}' | grep -q terminated; do sleep 1; done
for i in {1..5}; do oc rsync -c push-results ` + outdir + `/. $(hostname):` + outdir + ` && break; sleep 10; done`
}
