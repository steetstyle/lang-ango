package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	annotationKey = "langango-enabled"
	sidecarImage  = "langango/agent:latest"
)

type patch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

func main() {
	http.HandleFunc("/mutate", mutateHandler)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8443"
	}
	fmt.Printf("Lang-Ango Sidecar Injector listening on %s\n", port)
	http.ListenAndServe(":"+port, nil)
}

func mutateHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	admReq := v1.AdmissionReview{}
	if err := json.Unmarshal(body, &admReq); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	admResp := mutatePod(admReq)
	respBytes, err := json.Marshal(admResp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func mutatePod(admReq v1.AdmissionReview) *v1.AdmissionReview {
	pod := corev1.Pod{}
	if err := json.Unmarshal(admReq.Request.Object.Raw, &pod); err != nil {
		return admissionError(admReq, err)
	}

	// Check if annotation is present
	enabled := false
	for key, value := range pod.Annotations {
		if key == annotationKey && value == "true" {
			enabled = true
			break
		}
	}

	// Also check labels
	if !enabled {
		for key, value := range pod.Labels {
			if key == annotationKey && value == "true" {
				enabled = true
				break
			}
		}
	}

	if !enabled {
		return &v1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "admission.k8s.io/v1",
				Kind:       "AdmissionReview",
			},
			Response: &v1.AdmissionResponse{
				Allowed: true,
			},
		}
	}

	// Check if sidecar already exists
	for _, c := range pod.Spec.Containers {
		if c.Name == "langango-agent" {
			return &v1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "admission.k8s.io/v1",
					Kind:       "AdmissionReview",
				},
				Response: &v1.AdmissionResponse{
					Allowed: true,
				},
			}
		}
	}

	// Create sidecar container
	sidecar := corev1.Container{
		Name:            "langango-agent",
		Image:           sidecarImage,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://jaeger:4317"},
			{Name: "LANGANGO_SERVICE_NAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			}},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *parseQuantity("100m"),
				corev1.ResourceMemory: *parseQuantity("128Mi"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    *parseQuantity("10m"),
				corev1.ResourceMemory: *parseQuantity("32Mi"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: boolPtr(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	}

	// Create volume for socket
	emptyDirVolume := corev1.Volume{
		Name: "langango-socket",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}

	patches := []patch{
		{Op: "add", Path: "/spec/volumes", Value: []corev1.Volume{emptyDirVolume}},
		{Op: "add", Path: "/spec/containers/-", Value: sidecar},
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return admissionError(admReq, err)
	}

	return &v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &v1.AdmissionResponse{
			Allowed:   true,
			Patch:     patchBytes,
			PatchType: func() *v1.PatchType { pt := v1.PatchTypeJSONPatch; return &pt }(),
		},
	}
}

func admissionError(admReq v1.AdmissionReview, err error) *v1.AdmissionReview {
	return &v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &v1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		},
	}
}

func parseQuantity(s string) *resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return &q
}

func boolPtr(b bool) *bool {
	return &b
}
