package yunikorn

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	v1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/utils"
)

// TaskGroups is a list of task Groups recognized as gang Groups
type TaskGroups struct {
	Groups []TaskGroup `json:"groups"`
}

// TaskGroup is the struct for yunikorn to consider a pod belongs to a gang group
// the original schema is defined here: https://github.com/apache/yunikorn-k8shim/blob/master/pkg/cache/amprotocol.go
type TaskGroup struct {
	MinResource               map[string]resource.Quantity      `json:"minResource"`
	NodeSelector              map[string]string                 `json:"nodeSelector,omitempty"`
	Affinity                  *corev1.Affinity                  `json:"affinity,omitempty"`
	Name                      string                            `json:"name"`
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	Tolerations               []corev1.Toleration               `json:"tolerations,omitempty"`
	MinMember                 int32                             `json:"minMember"`
}

func newTaskGroups() *TaskGroups {
	return &TaskGroups{
		Groups: make([]TaskGroup, 0),
	}
}

func newTaskGroupsFromApp(app *v1.RayCluster) *TaskGroups {
	taskGroups := newTaskGroups()

	// head group
	headGroupSpec := app.Spec.HeadGroupSpec
	headPodMinResource := utils.CalculatePodResource(headGroupSpec.Template.Spec)
	taskGroups.addTaskGroup(
		TaskGroup{
			Name:         utils.RayNodeHeadGroupLabelValue,
			MinMember:    1,
			MinResource:  utils.ConvertResourceListToMapString(headPodMinResource),
			NodeSelector: headGroupSpec.Template.Spec.NodeSelector,
			Tolerations:  headGroupSpec.Template.Spec.Tolerations,
			Affinity:     headGroupSpec.Template.Spec.Affinity,
		})

	// worker groups
	for _, workerGroupSpec := range app.Spec.WorkerGroupSpecs {
		workerMinResource := utils.CalculatePodResource(workerGroupSpec.Template.Spec)
		minWorkers := workerGroupSpec.MinReplicas
		taskGroups.addTaskGroup(
			TaskGroup{
				Name:         workerGroupSpec.GroupName,
				MinMember:    *minWorkers,
				MinResource:  utils.ConvertResourceListToMapString(workerMinResource),
				NodeSelector: workerGroupSpec.Template.Spec.NodeSelector,
				Tolerations:  workerGroupSpec.Template.Spec.Tolerations,
				Affinity:     workerGroupSpec.Template.Spec.Affinity,
			})
	}

	return taskGroups
}

func newTaskGroupsFromRayJob(app *v1.RayJob, submitterTemplate *corev1.PodTemplateSpec) (*TaskGroups, error) {
	taskGroups := newTaskGroups()

	// head group
	headGroupSpec := app.Spec.RayClusterSpec.HeadGroupSpec
	headPodMinResource := utils.CalculatePodResource(headGroupSpec.Template.Spec)
	taskGroups.addTaskGroup(
		TaskGroup{
			Name:         utils.RayNodeHeadGroupLabelValue,
			MinMember:    1,
			MinResource:  utils.ConvertResourceListToMapString(headPodMinResource),
			NodeSelector: headGroupSpec.Template.Spec.NodeSelector,
			Tolerations:  headGroupSpec.Template.Spec.Tolerations,
			Affinity:     headGroupSpec.Template.Spec.Affinity,
		})

	// worker groups
	for _, workerGroupSpec := range app.Spec.RayClusterSpec.WorkerGroupSpecs {
		workerMinResource := utils.CalculatePodResource(workerGroupSpec.Template.Spec)
		minWorkers := workerGroupSpec.MinReplicas
		taskGroups.addTaskGroup(
			TaskGroup{
				Name:         workerGroupSpec.GroupName,
				MinMember:    *minWorkers,
				MinResource:  utils.ConvertResourceListToMapString(workerMinResource),
				NodeSelector: workerGroupSpec.Template.Spec.NodeSelector,
				Tolerations:  workerGroupSpec.Template.Spec.Tolerations,
				Affinity:     workerGroupSpec.Template.Spec.Affinity,
			})
	}

	var submitterGroupSpec corev1.PodSpec
	if submitterTemplate == nil {
		return nil, fmt.Errorf("submitter template should not be nil when creating task groups from RayJob")
	}
	submitterGroupSpec = submitterTemplate.Spec

	submitterPodMinResource := utils.CalculatePodResource(submitterGroupSpec)
	taskGroups.addTaskGroup(
		TaskGroup{
			Name:         utils.RayNodeSubmitterGroupLabelValue,
			MinMember:    1,
			MinResource:  utils.ConvertResourceListToMapString(submitterPodMinResource),
			NodeSelector: submitterGroupSpec.NodeSelector,
			Tolerations:  submitterGroupSpec.Tolerations,
			Affinity:     submitterGroupSpec.Affinity,
		})
	return taskGroups, nil
}

func (t *TaskGroups) size() int {
	return len(t.Groups)
}

func (t *TaskGroups) addTaskGroup(taskGroup TaskGroup) {
	t.Groups = append(t.Groups, taskGroup)
}

func (t *TaskGroups) marshal() (string, error) {
	result, err := json.Marshal(t.Groups)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (t *TaskGroups) unmarshalFrom(spec string) error {
	return json.Unmarshal([]byte(spec), &t.Groups)
}

func (t *TaskGroups) getTaskGroup(name string) TaskGroup {
	for _, group := range t.Groups {
		if group.Name == name {
			return group
		}
	}
	return TaskGroup{}
}
