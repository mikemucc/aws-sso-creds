package awsssocreds

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/JorgeReus/aws-sso-creds/internal/app/config"
	"github.com/JorgeReus/aws-sso-creds/internal/pkg/ui"
	"github.com/JorgeReus/aws-sso-creds/internal/pkg/util"
	"github.com/spf13/cobra"
)

var createStatic, populateRoles, foceLogin, noBrowser bool
var configPath, home string
var selectedOrg config.Organization

var rootCmd = &cobra.Command{
	Use:   "aws-sso-creds [flags] [organization]",
	Short: "aws-sso-creds - Local AWS SSO credentials made easy",
	Long: `Opinionated CLI app for AWS SSO made in Golang!
AWS SSO Creds is an AWS SSO creds manager for the shell.
Use it to easily manage entries in ~/.aws/config & ~/.aws/credentials files, so you can focus on your AWS workflows, without the hasslele of manually managing your credentials.`,
	Args: func(cmd *cobra.Command, args []string) error {

		if err := config.Init(home, configPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		// Validate exactly one arg for the org
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return err
		}

		var ok bool
		if selectedOrg, ok = config.GetInstance().Orgs[args[0]]; !ok {
			return fmt.Errorf(
				"organization '%s' not found in config file %s",
				args[0],
				configPath,
			)
		}
		return nil
	},
	PreRun: func(cmd *cobra.Command, args []string) {
		if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
			panic(fmt.Errorf("%s does not exist", configPath))

		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		uiVars := ui.UI{
			CreateStatic:  createStatic,
			PopulateRoles: populateRoles,
			ForceLogin:    foceLogin,
			NoBrowser:     noBrowser,
			Org:           selectedOrg,
		}

		if err := uiVars.Start(); err != nil {
			log.Fatal(err)
		}
	},
}

func Execute() {

	// temp
	rootCmd.Flags().
		BoolVarP(&createStatic, "temp", "t", false, "Create temporary credentials in ~/.aws/credentials")

	// roles
	rootCmd.Flags().
		BoolVarP(&populateRoles, "populateRoles", "p", false, "Populate AWS SSO roles in ~/.aws/config")

	// forceAuth
	rootCmd.Flags().
		BoolVarP(&foceLogin, "forceAuth", "f", false, "Force Authentication with AWS SSO")

	// noBrowser
	rootCmd.Flags().
		BoolVarP(&noBrowser, "noBrowser", "b", false, "Do not open in the browser automatically")

	var err error
	home, err = util.HomeDir()
	if err != nil {
		panic(fmt.Errorf("error getting user home dir: %s", err))
	}

	// configPath
	rootCmd.PersistentFlags().
		StringVarP(&configPath, "config", "c", fmt.Sprintf("%s/.config/aws-sso-creds.toml", home), "Directory of the .toml config")

	if err := rootCmd.Execute(); err != nil {
		panic(fmt.Errorf("there was an error running aws-sso-creds '%s'", err))
	}
}
