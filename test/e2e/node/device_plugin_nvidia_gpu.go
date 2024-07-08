/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package node

import (
	"context"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/test/e2e/framework"
	e2ejob "k8s.io/kubernetes/test/e2e/framework/job"
	imageutils "k8s.io/kubernetes/test/utils/image"
)

const (
	testPodNamePrefix = "nvidia-gpu-"
	// Nvidia driver installation can take upwards of 5 minutes.
	driverInstallTimeout = 10 * time.Minute
)

var (
	gpuResourceName v1.ResourceName
)

func makeCudaAdditionDevicePluginTestPod() *v1.Pod {
	podName := testPodNamePrefix + string(uuid.NewUUID())
	testContainers := []v1.Container{
		{
			Name:  "vector-addition-cuda8",
			Image: imageutils.GetE2EImage(imageutils.CudaVectorAdd),
			Resources: v1.ResourceRequirements{
				Limits: v1.ResourceList{
					gpuResourceName: *resource.NewQuantity(1, resource.DecimalSI),
				},
			},
		},
		{
			Name:  "vector-addition-cuda10",
			Image: imageutils.GetE2EImage(imageutils.CudaVectorAdd2),
			Resources: v1.ResourceRequirements{
				Limits: v1.ResourceList{
					gpuResourceName: *resource.NewQuantity(1, resource.DecimalSI),
				},
			},
		},
	}
	testPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
		},
	}

	testPod.Spec.Containers = testContainers
	if os.Getenv("TEST_MAX_GPU_COUNT") == "1" {
		testPod.Spec.Containers = []v1.Container{testContainers[0]}
	}
	framework.Logf("testPod.Spec.Containers {%#v}", testPod.Spec.Containers)
	return testPod
}

// startJob starts a simple CUDA job that requests gpu and the specified number of completions
func startJob(ctx context.Context, f *framework.Framework, completions int32) {
	var activeSeconds int64 = 3600
	testJob := e2ejob.NewTestJob("succeed", "cuda-add", v1.RestartPolicyAlways, 1, completions, &activeSeconds, 6)
	testJob.Spec.Template.Spec = v1.PodSpec{
		RestartPolicy: v1.RestartPolicyOnFailure,
		Containers: []v1.Container{
			{
				Name:    "vector-addition",
				Image:   imageutils.GetE2EImage(imageutils.CudaVectorAdd),
				Command: []string{"/bin/sh", "-c", "./vectorAdd && sleep 60"},
				Resources: v1.ResourceRequirements{
					Limits: v1.ResourceList{
						gpuResourceName: *resource.NewQuantity(1, resource.DecimalSI),
					},
				},
			},
		},
	}
	ns := f.Namespace.Name
	_, err := e2ejob.CreateJob(ctx, f.ClientSet, ns, testJob)
	framework.ExpectNoError(err)
	framework.Logf("Created job %v", testJob)
}
