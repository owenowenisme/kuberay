package yunikorn

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	schedulerinterface "github.com/ray-project/kuberay/ray-operator/controllers/ray/batchscheduler/interface"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/utils"
)

const (
	SchedulerName                       string = "yunikorn"
	YuniKornPodApplicationIDLabelName   string = "applicationId"
	YuniKornPodQueueLabelName           string = "queue"
	RayApplicationIDLabelName           string = "yunikorn.apache.org/app-id"
	RayApplicationQueueLabelName        string = "yunikorn.apache.org/queue"
	YuniKornTaskGroupNameAnnotationName string = "yunikorn.apache.org/task-group-name"
	YuniKornTaskGroupsAnnotationName    string = "yunikorn.apache.org/task-groups"
)

type YuniKornScheduler struct{}

type YuniKornSchedulerFactory struct{}

func GetPluginName() string {
	return SchedulerName
}

func (y *YuniKornScheduler) Name() string {
	return GetPluginName()
}

func (y *YuniKornScheduler) DoBatchSchedulingOnSubmission(_ context.Context, _ client.Object) error {
	// yunikorn doesn't require any resources to be created upfront
	// this is a no-opt for this implementation
	return nil
}

// populatePodLabels is a helper function that copies RayCluster's label to the given pod based on the label key
// TODO: remove the legacy labels, i.e "applicationId" and "queue", directly populate labels
// RayApplicationIDLabelName to RayApplicationQueueLabelName to pod labels.
// Currently we use this function to translate labels "yunikorn.apache.org/app-id" and "yunikorn.apache.org/queue"
// to legacy labels "applicationId" and "queue", this is for the better compatibilities to support older yunikorn
// versions.
func (y *YuniKornScheduler) populatePodLabelsFromRayCluster(ctx context.Context, rayCluster *rayv1.RayCluster, pod *corev1.Pod, sourceKey string, targetKey string) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	// check labels
	if value, exist := rayCluster.Labels[sourceKey]; exist {
		logger.Info("Updating pod label based on RayCluster labels",
			"sourceKey", sourceKey, "targetKey", targetKey, "value", value)
		pod.Labels[targetKey] = value
	}
}

// populateRayClusterLabelsFromRayJob is a helper function that copies RayJob's label to the given RayCluster based on the label key
func (y *YuniKornScheduler) populateRayClusterLabelsFromRayJob(ctx context.Context, rayJob *rayv1.RayJob, rayCluster *rayv1.RayCluster, sourceKey string, targetKey string) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	if value, exist := rayJob.Labels[sourceKey]; exist {
		logger.Info("Updating RayCluster label based on RayJob labels",
			"sourceKey", sourceKey, "targetKey", targetKey, "value", value)
		rayCluster.Labels[targetKey] = value
	}
}

// populateSubmitterPodTemplateLabelsFromRayJob adds essential labels and annotations to the Submitter pod
// the yunikorn scheduler needs these labels and annotations in order to do the scheduling properly
func (y *YuniKornScheduler) populateSubmitterPodTemplateLabelsFromRayJob(ctx context.Context, rayJob *rayv1.RayJob, submitterTemplate *corev1.PodTemplateSpec, sourceKey string, targetKey string) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	if value, exist := rayJob.Labels[sourceKey]; exist {
		logger.Info("Updating submitter pod template label based on RayJob labels",
			"sourceKey", sourceKey, "targetKey", targetKey, "value", value)
		submitterTemplate.Labels[targetKey] = value
	}
}

// AddMetadataToPodFromRayCluster adds essential labels and annotations to the Ray pod
// the yunikorn scheduler needs these labels and annotations in order to do the scheduling properly
func (y *YuniKornScheduler) AddMetadataToPodFromRayCluster(ctx context.Context, rayCluster *rayv1.RayCluster, groupName string, pod *corev1.Pod) {
	// the applicationID and queue name must be provided in the labels
	y.populatePodLabelsFromRayCluster(ctx, rayCluster, pod, RayApplicationIDLabelName, YuniKornPodApplicationIDLabelName)
	y.populatePodLabelsFromRayCluster(ctx, rayCluster, pod, RayApplicationQueueLabelName, YuniKornPodQueueLabelName)
	pod.Spec.SchedulerName = y.Name()

	// when gang scheduling is enabled, extra annotations need to be added to all pods
	if y.isGangSchedulingEnabled(rayCluster) {
		// populate the taskGroups info to each pod
		y.populateTaskGroupsAnnotationToPod(ctx, rayCluster, pod)

		// set the task group name based on the head or worker group name
		// the group name for the head and each of the worker group should be different
		pod.Annotations[YuniKornTaskGroupNameAnnotationName] = groupName
	}
}

func (y *YuniKornScheduler) AddMetadataToRayClusterFromRayJob(ctx context.Context, rayJob *rayv1.RayJob, rayCluster *rayv1.RayCluster, submitterTemplate *corev1.PodTemplateSpec) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	// the applicationID and queue name must be provided in the labels
	y.populateRayClusterLabelsFromRayJob(ctx, rayJob, rayCluster, RayApplicationIDLabelName, RayApplicationIDLabelName)
	y.populateRayClusterLabelsFromRayJob(ctx, rayJob, rayCluster, RayApplicationQueueLabelName, RayApplicationQueueLabelName)

	// when gang scheduling is enabled, extra annotations need to be added to all pods
	if y.isGangSchedulingEnabled(rayJob) {
		// populate the taskGroups info to RayCluster
		if rayJob.Spec.RayClusterSpec == nil {
			logger.Info("RayJob does not have RayClusterSpec, skip adding task groups annotation to RayCluster")
			return
		}
		y.populateTaskGroupsAnnotationToRayCluster(ctx, rayJob, rayCluster, submitterTemplate)
		logger.Info("Gang Scheduling enabled for RayJob")
	}
}

func (y *YuniKornScheduler) AddMetadataToSubmitterPodTemplateFromRayJob(ctx context.Context, rayJob *rayv1.RayJob, submitterTemplate *corev1.PodTemplateSpec) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	if submitterTemplate.Labels == nil {
		submitterTemplate.Labels = make(map[string]string)
	}
	// the applicationID and queue name must be provided in the labels
	y.populateSubmitterPodTemplateLabelsFromRayJob(ctx, rayJob, submitterTemplate, RayApplicationIDLabelName, YuniKornPodApplicationIDLabelName)
	y.populateSubmitterPodTemplateLabelsFromRayJob(ctx, rayJob, submitterTemplate, RayApplicationQueueLabelName, YuniKornPodQueueLabelName)
	submitterTemplate.Spec.SchedulerName = y.Name()

	// when gang scheduling is enabled, extra annotations need to be added to all pods
	if y.isGangSchedulingEnabled(rayJob) {
		// populate the taskGroups info to RayCluster and submitter pod template
		if rayJob.Spec.RayClusterSpec == nil {
			logger.Info("RayJob does not have RayClusterSpec, skip adding task groups annotation to submitter pod template")
			return
		}
		y.populateTaskGroupsAnnotationToSubmitterPodTemplate(ctx, rayJob, submitterTemplate)
		submitterTemplate.Annotations[YuniKornTaskGroupNameAnnotationName] = utils.RayNodeSubmitterGroupLabelValue
	}
}

func (y *YuniKornScheduler) isGangSchedulingEnabled(obj client.Object) bool {
	switch obj := obj.(type) {
	case *rayv1.RayCluster:
		_, exist := obj.Labels[utils.RayClusterGangSchedulingEnabled]
		return exist
	case *rayv1.RayJob:
		_, exist := obj.Labels[utils.RayClusterGangSchedulingEnabled]
		return exist
	default:
		return false
	}
}

func (y *YuniKornScheduler) populateTaskGroupsAnnotationToPod(ctx context.Context, rayCluster *rayv1.RayCluster, pod *corev1.Pod) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	var taskGroupsAnnotationValue string
	var err error
	if rayCluster.Annotations[YuniKornTaskGroupsAnnotationName] != "" {
		taskGroupsAnnotationValue = rayCluster.Annotations[YuniKornTaskGroupsAnnotationName]
		logger.Info("using existing task groups annotation from RayCluster", "value", taskGroupsAnnotationValue)
	} else {
		taskGroups := newTaskGroupsFromRayCluster(rayCluster)
		taskGroupsAnnotationValue, err = taskGroups.marshal()
		if err != nil {
			logger.Error(err, "failed to add gang scheduling related annotations to pod, "+
				"gang scheduling will not be enabled for this workload",
				"name", pod.Name, "namespace", pod.Namespace)
			return
		}

		logger.Info("add task groups info to pod's annotation",
			"key", YuniKornTaskGroupsAnnotationName,
			"value", taskGroupsAnnotationValue,
			"numOfTaskGroups", taskGroups.size())

	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	pod.Annotations[YuniKornTaskGroupsAnnotationName] = taskGroupsAnnotationValue
	logger.Info("Gang Scheduling enabled for RayCluster")
}

func (y *YuniKornScheduler) populateTaskGroupsAnnotationToRayCluster(ctx context.Context, rayJob *rayv1.RayJob, rayCluster *rayv1.RayCluster, submitterTemplate *corev1.PodTemplateSpec) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	taskGroups, err := newTaskGroupsFromRayJob(rayJob, submitterTemplate)
	if err != nil {
		logger.Error(err, "failed to create task groups from RayJob", "RayJob", rayJob.Name, "namespace", rayJob.Namespace)
		return
	}
	taskGroupsAnnotationValue, err := taskGroups.marshal()
	if err != nil {
		logger.Error(err, "failed to add gang scheduling related annotations to RayCluster, "+
			"gang scheduling will not be enabled for this workload",
			"name", rayCluster.Name, "namespace", rayCluster.Namespace)
		return
	}

	logger.Info("adding task groups info to RayCluster's annotation",
		"key", YuniKornTaskGroupsAnnotationName,
		"value", taskGroupsAnnotationValue,
		"numOfTaskGroups", taskGroups.size())

	if rayCluster.Annotations == nil {
		rayCluster.Annotations = make(map[string]string)
	}

	rayCluster.Annotations[YuniKornTaskGroupsAnnotationName] = taskGroupsAnnotationValue
	logger.Info("Gang Scheduling enabled for RayCluster")
}

func (y *YuniKornScheduler) populateTaskGroupsAnnotationToSubmitterPodTemplate(ctx context.Context, rayJob *rayv1.RayJob, submitterTemplate *corev1.PodTemplateSpec) {
	logger := ctrl.LoggerFrom(ctx).WithName(SchedulerName)
	taskGroups, err := newTaskGroupsFromRayJob(rayJob, submitterTemplate)
	if err != nil {
		logger.Error(err, "failed to create task groups from RayJob", "RayJob", rayJob.Name, "namespace", rayJob.Namespace)
		return
	}
	taskGroupsAnnotationValue, err := taskGroups.marshal()
	if err != nil {
		logger.Error(err, "failed to add gang scheduling related annotations to SubmitterPodTemplate")
		return
	}

	logger.Info("adding task groups info to SubmitterPodTemplate's annotation",
		"key", YuniKornTaskGroupsAnnotationName,
		"value", taskGroupsAnnotationValue,
		"numOfTaskGroups", taskGroups.size())

	if submitterTemplate.Annotations == nil {
		submitterTemplate.Annotations = make(map[string]string)
	}

	submitterTemplate.Annotations[YuniKornTaskGroupsAnnotationName] = taskGroupsAnnotationValue
	logger.Info("Gang Scheduling enabled for submitter pod template")
}

func (yf *YuniKornSchedulerFactory) New(_ context.Context, _ *rest.Config, _ client.Client) (schedulerinterface.BatchScheduler, error) {
	return &YuniKornScheduler{}, nil
}

func (yf *YuniKornSchedulerFactory) AddToScheme(_ *runtime.Scheme) {
	// No extra scheme needs to be registered
}

func (yf *YuniKornSchedulerFactory) ConfigureReconciler(b *builder.Builder) *builder.Builder {
	return b
}
