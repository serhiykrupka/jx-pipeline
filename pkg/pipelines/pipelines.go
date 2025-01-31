package pipelines

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/jenkins-x/jx-api/v4/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx-helpers/v3/pkg/kube/activities"
	"github.com/jenkins-x/jx-helpers/v3/pkg/kube/naming"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	OwnerLabels   = []string{"owner", "lighthouse.jenkins-x.io/refs.org"}
	RepoLabels    = []string{"repository", "lighthouse.jenkins-x.io/refs.repo"}
	BranchLabels  = []string{"branch", "lighthouse.jenkins-x.io/branch"}
	BuildLabels   = []string{"build", "lighthouse.jenkins-x.io/buildNum"}
	ContextLabels = []string{"context", "lighthouse.jenkins-x.io/context"}
)

// GetLabel returns the first label value for the given strings
func GetLabel(m map[string]string, labels []string) string {
	if m == nil {
		return ""
	}
	for _, l := range labels {
		value := m[l]
		if value != "" {
			return value
		}
	}
	return ""
}

// DefaultValues default missing values from the lighthouse labels
func DefaultValues(a *v1.PipelineActivity) {
	labels := a.Labels
	if labels != nil {
		if a.Spec.GitOwner == "" {
			a.Spec.GitOwner = GetLabel(labels, OwnerLabels)
		}
		if a.Spec.GitRepository == "" {
			a.Spec.GitRepository = GetLabel(labels, RepoLabels)
		}
		if a.Spec.GitBranch == "" {
			a.Spec.GitBranch = GetLabel(labels, BranchLabels)
		}
		if a.Spec.Context == "" {
			a.Spec.Context = GetLabel(labels, ContextLabels)
		}
		if a.Spec.Build == "" {
			a.Spec.Build = GetLabel(labels, BuildLabels)
		}
	}
	if a.Spec.StartedTimestamp == nil {
		for _, s := range a.Spec.Steps {
			if s.Stage != nil {
				a.Spec.StartedTimestamp = s.Stage.StartedTimestamp
			} else if s.Promote != nil {
				a.Spec.StartedTimestamp = s.Promote.StartedTimestamp
			} else if s.Preview != nil {
				a.Spec.StartedTimestamp = s.Preview.StartedTimestamp
			}
			if a.Spec.StartedTimestamp != nil {
				break
			}
		}
	}
	if string(a.Spec.Status) == "" {
		// lets default the status to the last step if its missing
		for i := len(a.Spec.Steps) - 1; i > -0; i-- {
			s := a.Spec.Steps[i]
			status := v1.ActivityStatusTypeNone
			if s.Stage != nil {
				status = s.Stage.Status
			} else if s.Promote != nil {
				status = s.Promote.Status
			} else if s.Preview != nil {
				status = s.Preview.Status
			}

			if string(status) != "" {
				a.Spec.Status = status
				break
			}
		}
	}
}

// ToPipelineActivityName creates an activity name from a pipeline run
func ToPipelineActivityName(pr *v1beta1.PipelineRun, paList []v1.PipelineActivity) string {
	labels := pr.Labels
	if labels == nil {
		return ""
	}

	build := labels["build"]
	owner := GetLabel(labels, OwnerLabels)
	repository := GetLabel(labels, RepoLabels)
	branch := GetLabel(labels, BranchLabels)

	if owner == "" || repository == "" || branch == "" {
		return ""
	}

	prefix := owner + "-" + repository + "-" + branch + "-"
	if build == "" {
		buildID := labels["lighthouse.jenkins-x.io/buildNum"]
		if buildID == "" {
			return ""
		}
		for i := range paList {
			pa := &paList[i]
			if pa.Labels == nil {
				continue
			}
			if pa.Labels["buildID"] == buildID || pa.Labels["lighthouse.jenkins-x.io/buildNum"] == buildID {
				if pa.Spec.Build != "" {
					pr.Labels["build"] = pa.Spec.Build
					return pa.Name
				}
			}
		}

		// no PA has the buildNum yet so lets try find the next PA build number...
		b := 1
		for {
			build = strconv.Itoa(b)
			name := naming.ToValidName(prefix + build)
			found := false
			for i := range paList {
				pa := &paList[i]
				if pa.Name == name {
					found = true
					break
				}
			}
			if !found {
				pr.Labels["build"] = build
				return name
			}
			b++
		}
	}
	if build == "" {
		return ""
	}
	return naming.ToValidName(prefix + build)
}

func ToPipelineActivity(pr *v1beta1.PipelineRun, pa *v1.PipelineActivity, overwriteSteps bool) {
	annotations := pr.Annotations
	labels := pr.Labels
	if pa.APIVersion == "" {
		pa.APIVersion = "jenkins.io/v1"
	}
	if pa.Kind == "" {
		pa.Kind = "PipelineActivity"
	}
	pa.Namespace = pr.Namespace

	if pa.Annotations == nil {
		pa.Annotations = map[string]string{}
	}
	if pa.Labels == nil {
		pa.Labels = map[string]string{}
	}
	for k, v := range annotations {
		switch k {
		case "lighthouse.jenkins-x.io/traceparent", "lighthouse.jenkins-x.io/tracestate":
			// the opentelemetry annotations holding trace context shouldn't be copied to other resources
		default:
			pa.Annotations[k] = v
		}
	}
	for k, v := range labels {
		pa.Labels[k] = v
	}

	ps := &pa.Spec
	if labels != nil {
		if ps.GitOwner == "" {
			ps.GitOwner = GetLabel(labels, OwnerLabels)
		}
		if ps.GitRepository == "" {
			ps.GitRepository = GetLabel(labels, RepoLabels)
		}
		if ps.GitBranch == "" {
			ps.GitBranch = GetLabel(labels, BranchLabels)
		}
		if ps.Build == "" {
			ps.Build = GetLabel(labels, BuildLabels)
		}
		if ps.Context == "" {
			ps.Context = GetLabel(labels, ContextLabels)
		}
		if ps.BaseSHA == "" {
			ps.BaseSHA = labels["lighthouse.jenkins-x.io/baseSHA"]
		}
		if ps.LastCommitSHA == "" {
			ps.LastCommitSHA = labels["lighthouse.jenkins-x.io/lastCommitSHA"]
		}
	}
	if ps.GitOwner != "" && ps.GitRepository != "" && ps.GitBranch != "" && ps.Pipeline == "" {
		ps.Pipeline = fmt.Sprintf("%s/%s/%s", ps.GitOwner, ps.GitRepository, ps.GitBranch)
	}
	if annotations != nil {
		if ps.GitURL == "" {
			ps.GitURL = annotations["lighthouse.jenkins-x.io/cloneURI"]
		}
	}

	podName := ""
	stageNames := map[string]bool{}
	var steps []v1.PipelineActivityStep
	if pr.Status.TaskRuns != nil {
		for _, v := range pr.Status.TaskRuns {
			stageName := strings.ReplaceAll(v.PipelineTaskName, "-", " ")
			stageNames[stageName] = true
			var stage *v1.PipelineActivityStep
			if v.Status == nil {
				continue
			}
			if podName == "" {
				podName = v.Status.PodName
			}

			previousStepTerminated := false
			for _, step := range v.Status.Steps {
				name := step.Name
				var started *metav1.Time
				var completed *metav1.Time
				status := v1.ActivityStatusTypePending

				terminated := step.Terminated
				if terminated != nil {
					if terminated.ExitCode == 0 {
						status = v1.ActivityStatusTypeSucceeded
					} else if !terminated.FinishedAt.IsZero() {
						status = v1.ActivityStatusTypeFailed
					}
					started = &terminated.StartedAt
					completed = &terminated.FinishedAt
					previousStepTerminated = true
				} else if step.Running != nil {
					if previousStepTerminated {
						started = &step.Running.StartedAt
						status = v1.ActivityStatusTypeRunning
					}
					previousStepTerminated = false
				}

				if status.IsTerminated() && completed == nil {
					completed = &metav1.Time{
						Time: time.Now(),
					}
				}

				step := v1.CoreActivityStep{
					Name:               Humanize(name),
					Description:        "",
					Status:             status,
					StartedTimestamp:   started,
					CompletedTimestamp: completed,
				}

				if stage == nil {
					stage = &v1.PipelineActivityStep{
						Kind: v1.ActivityStepKindTypeStage,
						Stage: &v1.StageActivityStep{
							CoreActivityStep: v1.CoreActivityStep{
								//Name:               Humanize(stageName),
								Name:             stageName,
								Description:      "",
								Status:           status,
								StartedTimestamp: started,
							},
						},
					}
				}
				stage.Stage.Steps = append(stage.Stage.Steps, step)
			}
			if stage != nil {
				// lets check we have a started time if we have at least 1 step
				if stage.Stage != nil && len(stage.Stage.Steps) > 0 {
					if stage.Stage.Steps[0].StartedTimestamp == nil {
						stage.Stage.Steps[0].StartedTimestamp = &metav1.Time{
							Time: time.Now(),
						}
					}
					if stage.Stage.StartedTimestamp == nil {
						stage.Stage.StartedTimestamp = stage.Stage.Steps[0].StartedTimestamp
					}
					// lets check the last step
					lastStep := stage.Stage.Steps[len(stage.Stage.Steps)-1]
					if stage.Stage.CompletedTimestamp == nil {
						stage.Stage.CompletedTimestamp = lastStep.CompletedTimestamp
					}
				}
				steps = append(steps, *stage)
			}
		}
	}

	if overwriteSteps {
		for _, stage := range steps {
			if stage.Stage == nil {
				continue
			}
			idx := -1
			found := false
			for i := range ps.Steps {
				s := &ps.Steps[i]
				if s.Stage != nil && s.Stage.Name == stage.Stage.Name {
					s.Stage = stage.Stage
					found = true
					break
				}
				if s.Kind == v1.ActivityStepKindTypePreview || s.Kind == v1.ActivityStepKindTypePromote {
					if idx < 9 {
						idx = i
					}
				}
			}
			if !found {
				if idx < 0 {
					ps.Steps = append(ps.Steps, stage)
				} else {
					// lets add the new stage before the preview/promote stages
					var remaining []v1.PipelineActivityStep
					if idx < len(ps.Steps) {
						remaining = ps.Steps[idx:]
					}
					ps.Steps = append(ps.Steps[0:idx], stage)
					ps.Steps = append(ps.Steps, remaining...)
				}
			}
		}
	} else {
		// if the PipelineActivity has some real steps lets trust it; otherwise lets merge any preview/promote steps
		// with steps from the PipelineRun
		// lets add any missing steps from the PipelineActivity as they may have been created via a `jx promote` step
		hasStep := false
		for _, s := range ps.Steps {
			if s.Kind == v1.ActivityStepKindTypeStage && s.Stage != nil && s.Stage.Name != "Release" {
				hasStep = true
				break
			}
		}
		if !hasStep {
			for _, s := range ps.Steps {
				if s.Kind == v1.ActivityStepKindTypePreview || s.Kind == v1.ActivityStepKindTypePromote {
					steps = append(steps, s)
				}
			}
			ps.Steps = steps
		}
	}

	if len(ps.Steps) > 0 && ps.StartedTimestamp == nil {
		// lets default a start time
		if ps.Steps[0].Stage != nil {
			ps.StartedTimestamp = ps.Steps[0].Stage.StartedTimestamp
		}
	}
	if ps.StartedTimestamp == nil {
		ps.StartedTimestamp = &metav1.Time{
			Time: time.Now(),
		}
	}

	if len(ps.Steps) == 0 && !overwriteSteps {
		ps.Steps = append(ps.Steps, v1.PipelineActivityStep{
			Kind: v1.ActivityStepKindTypeStage,
			Stage: &v1.StageActivityStep{
				CoreActivityStep: v1.CoreActivityStep{
					Name:   "initialising",
					Status: v1.ActivityStatusTypeRunning,
				},
			},
		})
	}

	if podName != "" {
		pa.Labels["podName"] = podName
	}

	activities.UpdateStatus(pa, false, nil)
}

// Humanize splits into words and capitalises
func Humanize(text string) string {
	wordsText := strings.ReplaceAll(strings.ReplaceAll(text, "-", " "), "_", " ")
	words := strings.Split(wordsText, " ")
	for i := range words {
		words[i] = strings.Title(words[i])
	}
	return strings.Join(words, " ")
}
