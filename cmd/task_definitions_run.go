package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TylerBrock/colorjson"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type outputConfiguration struct {
	Expand         bool
	Raw            bool
	RawString      bool
	HideStreamName bool
	HideDate       bool
	Invert         bool
	NoColor        bool
}

func (c *outputConfiguration) Formatter() *colorjson.Formatter {
	formatter := colorjson.NewFormatter()

	if c.Expand {
		formatter.Indent = 4
	}

	if c.RawString {
		formatter.RawStrings = true
	}

	if c.Invert {
		formatter.KeyColor = color.New(color.FgBlack)
	}

	if c.NoColor {
		color.NoColor = true
	}

	return formatter
}

func printEvent(formatter *colorjson.Formatter, event *cloudwatchlogs.FilteredLogEvent) {
	red := color.New(color.FgRed).SprintFunc()
	white := color.New(color.FgWhite).SprintFunc()

	str := aws.StringValue(event.Message)
	bytes := []byte(str)
	date := aws.MillisecondsTimeValue(event.Timestamp)
	dateStr := date.Format(time.RFC3339)
	streamStr := aws.StringValue(event.LogStreamName)
	jl := map[string]interface{}{}
	if err := json.Unmarshal(bytes, &jl); err != nil {
		fmt.Printf("[%s] (%s) %s\n", red(dateStr), white(streamStr), str)
	} else {
		output, _ := formatter.Marshal(jl)
		fmt.Printf("[%s] (%s) %s\n", red(dateStr), white(streamStr), output)
	}
}

func taskDefinitionsRunRun(cmd *cobra.Command, args []string) {
	taskDefinitionFamily := args[0]

	tdDescription, err := ecsI.DescribeTaskDefinition(&ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefinitionFamily),
	})
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	td := tdDescription.TaskDefinition

	if revision == "" {
		revision = strconv.FormatInt(aws.Int64Value(td.Revision), 10)
	}

	taskResult, err := ecsI.RunTask(&ecs.RunTaskInput{
		Cluster:        aws.String(cluster),
		TaskDefinition: aws.String(taskDefinitionFamily + ":" + revision),
		StartedBy:      aws.String("ecsctl"),
	})
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	if len(taskResult.Tasks) == 0 {
		fmt.Println(errors.New("task failed to run"))
		os.Exit(1)
	}

	if !follow {
		os.Exit(0)
	}

	if exit {
		var gracefulStop = make(chan os.Signal)
		signal.Notify(gracefulStop, syscall.SIGTERM)
		signal.Notify(gracefulStop, syscall.SIGINT)
		go func() {
			<-gracefulStop

			ecsI.StopTask(&ecs.StopTaskInput{
				Cluster: aws.String(cluster),
				Task:    taskResult.Tasks[0].TaskArn,
			})

			os.Exit(0)
		}()
	}

	tSplited := strings.Split(aws.StringValue(taskResult.Tasks[0].TaskArn), "/")
	taskID := tSplited[1]

	logDriver := td.ContainerDefinitions[0].LogConfiguration.LogDriver
	if aws.StringValue(logDriver) != "awslogs" {
		os.Exit(0)
	}

	logPrefix := td.ContainerDefinitions[0].LogConfiguration.Options["awslogs-stream-prefix"]
	logGroup := td.ContainerDefinitions[0].LogConfiguration.Options["awslogs-group"]

	cName := td.ContainerDefinitions[0].Name
	logStreamName := aws.StringValue(logPrefix) + "/" + aws.StringValue(cName) + "/" + taskID

	var lastSeenTime *int64
	var seenEventIDs map[string]bool
	output := outputConfiguration{}
	formatter := output.Formatter()

	clearSeenEventIds := func() {
		seenEventIDs = make(map[string]bool, 0)
	}

	addSeenEventIDs := func(id *string) {
		seenEventIDs[*id] = true
	}

	updateLastSeenTime := func(ts *int64) {
		if lastSeenTime == nil || *ts > *lastSeenTime {
			lastSeenTime = ts
			clearSeenEventIds()
		}
	}

	cwInput := cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:   logGroup,
		LogStreamNames: []*string{aws.String(logStreamName)},
	}

	handlePage := func(page *cloudwatchlogs.FilterLogEventsOutput, lastPage bool) bool {
		for _, event := range page.Events {
			updateLastSeenTime(event.Timestamp)
			if _, seen := seenEventIDs[*event.EventId]; !seen {
				printEvent(formatter, event)
				addSeenEventIDs(event.EventId)
			}
		}
		return !lastPage
	}

	retryCount := 0
	retryLimit := 50
	for {
		err := cwlI.FilterLogEventsPages(&cwInput, handlePage)
		if err != nil {
			retryCount = retryCount + 1

			if retryCount >= retryLimit {
				fmt.Println(err.Error())
				os.Exit(1)
			}
		}

		if lastSeenTime != nil {
			cwInput.SetStartTime(*lastSeenTime)
		}

		tasksStatus, err := ecsI.DescribeTasks(&ecs.DescribeTasksInput{
			Cluster: aws.String(cluster),
			Tasks:   []*string{aws.String(taskID)},
		})

		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		status := aws.StringValue(tasksStatus.Tasks[0].LastStatus)
		if status == "STOPPED" {
			os.Exit(0)
		}

		time.Sleep(1 * time.Second)
	}
}

var taskDefinitionsRunCmd = &cobra.Command{
	Use:   "run [task-definition]",
	Short: "Run a Task Definition",
	Args:  cobra.ExactArgs(1),
	Run:   taskDefinitionsRunRun,
}

func init() {
	taskDefinitionsCmd.AddCommand(taskDefinitionsRunCmd)

	flags := taskDefinitionsRunCmd.Flags()

	flags.BoolVar(&exit, "exit", false, exitSpec)

	flags.BoolVarP(&follow, "follow", "f", false, followSpec)

	flags.StringVar(&revision, "revision", "", revisionSpec)

	flags.StringVarP(&cluster, "cluster", "c", "", requiredSpec+clusterSpec)

	taskDefinitionsRunCmd.MarkFlagRequired("cluster")

	viper.BindPFlag("cluster", taskDefinitionsRunCmd.Flags().Lookup("cluster"))
}
