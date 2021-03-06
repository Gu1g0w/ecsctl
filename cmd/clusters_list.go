package cmd

import (
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/spf13/cobra"
)

func clustersListRun(cmd *cobra.Command, clusters []string) {
	input := &ecs.ListClustersInput{}

	var nextToken *string
	for {
		if nextToken != nil {
			input.NextToken = nextToken
		}

		result, err := ecsI.ListClusters(input)

		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		for _, f := range result.ClusterArns {
			fmt.Println(aws.StringValue(f))
		}

		if result.NextToken == nil {
			break
		}

		nextToken = result.NextToken
	}
}

var clustersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List clusters",
	Run:   clustersListRun,
}

func init() {
	clustersCmd.AddCommand(clustersListCmd)
}
