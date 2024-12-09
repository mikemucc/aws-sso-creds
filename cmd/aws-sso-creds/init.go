package awsssocreds

import "fmt"

// import(
// 	"github.com/pelletier/go-toml/v2"
// )

type Organization struct {
	Name   string `validate:"required"`
	Prefix string `validate:"required" mapstructure:"prefix"`
	URL    string `validate:"required" mapstructure:"url"`
	Region string `validate:"required" mapstructure:"region"`
}

func initConfig() {
	var name, url, region string
	fmt.Printf("No config file found at %s", configPath)
	fmt.Println("Creating initial config for you...")
	fmt.Println("What is the URL for your AWS signin page?")
	fmt.Scanln(&url)
	fmt.Println("What region is your AWS SSO set up in?")
	fmt.Scanln(&region)
	fmt.Println("What is the name of this AWS Organization?")
	fmt.Scanln(&name)

}
