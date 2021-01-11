package trace

import (
	"context"
	"fmt"
	"regexp"

	"github.com/profclems/glab/internal/glrepo"
	"github.com/profclems/glab/pkg/prompt"

	"github.com/profclems/glab/commands/ci/ciutils"
	"github.com/profclems/glab/commands/cmdutils"
	"github.com/profclems/glab/internal/git"
	"github.com/profclems/glab/internal/utils"
	"github.com/profclems/glab/pkg/api"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/spf13/cobra"
	"github.com/xanzy/go-gitlab"
)

type TraceOpts struct {
	Branch string
	JobID  int

	BaseRepo   func() (glrepo.Interface, error)
	HTTPClient func() (*gitlab.Client, error)
	IO         *utils.IOStreams
}

func NewCmdTrace(f *cmdutils.Factory, runE func(traceOpts *TraceOpts) error) *cobra.Command {
	opts := &TraceOpts{
		IO: f.IO,
	}
	var pipelineCITraceCmd = &cobra.Command{
		Use:   "trace [<job-id>] [flags]",
		Short: `Trace a CI job log in real time`,
		Example: heredoc.Doc(`
	$ glab ci trace
	#=> interactively select a job to trace

	$ glab ci trace 224356863
	#=> trace job with id 224356863
	`),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			// support `-R, --repo` override
			//
			// NOTE: it is important to assign the BaseRepo and HTTPClient in RunE because
			// they are overridden in a PersistentRun hook (when `-R, --repo` is specified)
			// which runs before RunE is executed
			opts.BaseRepo = f.BaseRepo
			opts.HTTPClient = f.HttpClient

			if len(args) != 0 {
				opts.JobID = utils.StringToInt(args[0])
			}
			if opts.Branch == "" {
				opts.Branch, err = git.CurrentBranch()
				if err != nil {
					return err
				}
			}
			if runE != nil {
				return runE(opts)
			}
			return TraceRun(opts)
		},
	}

	pipelineCITraceCmd.Flags().StringVarP(&opts.Branch, "branch", "b", "", "Check pipeline status for a branch. (Default is the current branch)")
	return pipelineCITraceCmd
}

func TraceRun(opts *TraceOpts) error {
	apiClient, err := opts.HTTPClient()
	if err != nil {
		return err
	}

	repo, err := opts.BaseRepo()
	if err != nil {
		return err
	}

	if opts.JobID < 1 {
		l := &gitlab.ListProjectPipelinesOptions{
			Ref:  gitlab.String(opts.Branch),
			Sort: gitlab.String("desc"),
		}

		l.Page = 1
		l.PerPage = 1

		fmt.Fprintf(opts.IO.StdOut, "\nSearching for latest pipeline on %s...\n", opts.Branch)

		pipes, err := api.GetPipelines(apiClient, l, repo.FullName())
		if err != nil {
			return err
		}

		if len(pipes) == 0 {
			fmt.Fprintln(opts.IO.StdOut, "No pipeline running or available on "+opts.Branch+"branch")
			return nil
		}

		pipeline := pipes[0]
		fmt.Fprintf(opts.IO.StdOut, "Getting jobs for pipeline %d...\n\n", pipeline.ID)

		jobs, err := api.GetPipelineJobs(apiClient, pipeline.ID, repo.FullName())
		if err != nil {
			return err
		}

		var jobOptions []string
		var selectedJob string

		for _, job := range jobs {
			jobOptions = append(jobOptions, fmt.Sprintf("%s (%d) - %s", job.Name, job.ID, job.Status))
		}

		promptOpts := &survey.Select{
			Message: "Select pipeline job to trace:",
			Options: jobOptions,
		}

		_ = prompt.AskOne(promptOpts, &selectedJob)

		if selectedJob != "" {
			re := regexp.MustCompile(`(?s)\((.*)\)`)
			m := re.FindAllStringSubmatch(selectedJob, -1)
			opts.JobID = utils.StringToInt(m[0][1])
		} else {
			opts.JobID = jobs[0].ID
		}
	}

	job, err := api.GetPipelineJob(apiClient, opts.JobID, repo.FullName())
	if err != nil {
		return err
	}
	fmt.Fprintln(opts.IO.StdOut)

	err = ciutils.RunTrace(context.Background(), apiClient, opts.IO.StdOut, repo.FullName(), job.Pipeline.Sha, job.Name)
	if err != nil {
		return err
	}

	return nil
}
