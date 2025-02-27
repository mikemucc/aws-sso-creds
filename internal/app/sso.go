package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mikemucc/aws-sso-creds/internal/app/config"
	"github.com/mikemucc/aws-sso-creds/internal/pkg/cache"
	"github.com/mikemucc/aws-sso-creds/internal/pkg/files"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sso"
	"github.com/aws/aws-sdk-go/service/ssooidc"
	"github.com/mikemucc/aws-sso-creds/internal/pkg/bus"
	"github.com/pkg/browser"
)

const (
	tempCredsPrefix = "tmp"
)

func Login(
	org config.Organization,
	forceLogin, noBrowser bool,
	msgBus *bus.Bus,
) (*SSOFlow, error) {
	session := session.Must(session.NewSession())
	ssoClient := ssooidc.New(session, aws.NewConfig().WithRegion(org.Region))
	clientCredentials, err := cache.GetSSOClientCreds(org.Region)
	if err != nil {
		return nil, err
	}

	// If theres no credentials to use, create new ones
	if clientCredentials == nil || forceLogin {
		input := ssooidc.RegisterClientInput{ClientName: &clientName, ClientType: &clientType}

		resp, err := ssoClient.RegisterClient(&input)
		if err != nil {
			return nil, err
		}

		tm := time.Unix(*resp.ClientSecretExpiresAt, 0)
		clientCredentials = &cache.SSOClientCredentials{
			ClientId:     *resp.ClientId,
			ClientSecret: *resp.ClientSecret,
			ExpiresAt:    tm.Format(time.RFC3339),
		}
		if clientCredentials.Save(&org.Region) != nil {
			return nil, err
		}
	}

	var ssoToken *cache.SSOToken
	ssoToken, err = cache.GetSSOToken(org.URL, session, ssoClient, org.Region)
	if err != nil {
		return nil, err
	}

	if ssoToken == nil || forceLogin {
		startDeviceAuthInput := ssooidc.StartDeviceAuthorizationInput{
			ClientId:     &clientCredentials.ClientId,
			ClientSecret: &clientCredentials.ClientSecret,
			StartUrl:     &org.URL,
		}
		response, err := ssoClient.StartDeviceAuthorization(&startDeviceAuthInput)
		if err != nil {
			return nil, err
		}

		msgBus.Send(bus.BusMsg{
			MsgType:  bus.MSG_TYPE_INFO,
			Contents: fmt.Sprintln(fmt.Sprintf("The code received is %s, please verify accordingly", *response.UserCode)),
		})

		if !noBrowser {
			err = browser.OpenURL(*response.VerificationUriComplete)
		}

		if err != nil || noBrowser {
			s := fmt.Sprintf(
				"Can't open your browser, open this URL mannually: %s",
				*response.VerificationUriComplete,
			)
			msgBus.Send(bus.BusMsg{
				MsgType:  bus.MSG_TYPE_ERR,
				Contents: s,
			})

			msgBus.Recv()
		}

		for {
			time.Sleep(time.Second * time.Duration(*response.Interval))
			createTokenInput := ssooidc.CreateTokenInput{
				ClientId:     &clientCredentials.ClientId,
				ClientSecret: &clientCredentials.ClientSecret,
				Code:         response.UserCode,
				DeviceCode:   response.DeviceCode,
				GrantType:    &grantType,
			}
			createTokenOutput, err := ssoClient.CreateToken(&createTokenInput)

			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					switch aerr.Code() {
					case ssooidc.ErrCodeAuthorizationPendingException:

					default:
						return nil, err
					}
				}
			} else {
				ssoToken = &cache.SSOToken{
					StartUrl:    org.URL,
					Region:      org.Region,
					AccessToken: *createTokenOutput.AccessToken,
					ExpiresAt:   time.Now().Add(time.Second * time.Duration(*createTokenOutput.ExpiresIn)).Format(time.RFC3339),
				}

				ssoToken.Save(org.URL)
				break
			}
		}
	}
	// Parse the expires time to a human readable output and print it
	t, err := time.Parse(time.RFC3339Nano, ssoToken.ExpiresAt)
	if err != nil {
		return nil, err
	}

	s := fmt.Sprintf("The SSO session will expire at %s", t)
	msgBus.Send(bus.BusMsg{
		MsgType:  bus.MSG_TYPE_INFO,
		Contents: s,
	})
	ssoServiceClient := sso.New(session, aws.NewConfig().WithRegion(org.Region))
	file, err := files.NewConfigFile(config.GetInstance().Home)
	if err != nil {
		return nil, err
	}

	return &SSOFlow{
		accessToken: &ssoToken.AccessToken,
		ssoClient:   ssoServiceClient,
		configFile:  file,
		ssoRegion:   &org.Region,
		ssoStartUrl: &org.URL,
		orgName:     org.Name,
		prefix:      org.Prefix,
	}, nil
}

func (s *SSOFlow) getAccountRoles(
	acc *sso.AccountInfo,
	wg *sync.WaitGroup,
	channel chan AccountRolesOutput,
) {
	var result AccountRolesOutput
	listRolesInput := sso.ListAccountRolesInput{
		AccessToken: s.accessToken,
		AccountId:   acc.AccountId,
		NextToken:   nil,
	}

	var roleList []*sso.RoleInfo

	for {
		rolesResponse, err := s.ssoClient.ListAccountRoles(&listRolesInput)
		if err != nil {
			result.err = err
			wg.Done()
			channel <- result
			return
		}
		for _, role := range rolesResponse.RoleList {
			roleList = append(roleList, role)
		}
		if rolesResponse.NextToken == nil {
			break
		}
		listRolesInput.NextToken = rolesResponse.NextToken
	}

	for _, role := range roleList {
		parts := strings.Split(*acc.AccountName, " ")
		var body string
		for i, s := range parts {
			if i > 0 {
				body += "-" + s
			} else {
				body += s
			}
		}
		sectionName := fmt.Sprintf("profile %s:%s:%s", s.prefix, body, *role.RoleName)

		section, err := s.configFile.File.NewSection(sectionName)
		if err != nil {
			result.err = err
			break
		}

		section.NewKey("sso_start_url", *s.ssoStartUrl)
		section.NewKey("sso_region", *s.ssoRegion)
		section.NewKey("sso_account_name", *acc.AccountName)
		section.NewKey("sso_account_id", *acc.AccountId)
		section.NewKey("sso_role_name", *role.RoleName)
		section.NewKey("region", *s.ssoRegion)
		section.NewKey("org", s.orgName)
		section.NewKey("sso_auto_populated", "true")
	}
	channel <- result
	wg.Done()
}

func (s *SSOFlow) PopulateRoles() ([]string, error) {
	listAccountsInput := sso.ListAccountsInput{
		AccessToken: s.accessToken,
		NextToken:   nil,
	}

	accounts := []*sso.AccountInfo{}

	for {
		accsResponse, err := s.ssoClient.ListAccounts(&listAccountsInput)
		if err != nil {
			return nil, err
		}

		accounts = append(accounts, accsResponse.AccountList...)

		if accsResponse.NextToken == nil {
			break
		}

		listAccountsInput.NextToken = accsResponse.NextToken
	}

	s.configFile.CleanTemporaryRoles(s.orgName)

	var wg sync.WaitGroup
	wg.Add(len(accounts))
	queue := make(chan AccountRolesOutput, len(accounts))
	for _, acc := range accounts {
		go s.getAccountRoles(acc, &wg, queue)
	}
	wg.Wait()
	var result []string
	for _, section := range s.configFile.File.Sections() {
		if files.IsValidEntry(section, s.orgName) {
			name := strings.Replace(section.Name(), "profile ", "", 1)
			result = append(result, name)
		}
	}
	err := s.configFile.Save()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SSOFlow) GetCredentials() ([]CredentialsResult, error) {
	var result []CredentialsResult
	creds, err := files.NewCredentialsFile(config.GetInstance().Home)
	creds.CleanTemporaryRoles(s.orgName)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	queue := make(chan RoleCredentialsOutput, len(s.configFile.File.Sections()))
	for _, section := range s.configFile.File.Sections() {
		if files.IsValidEntry(section, s.orgName) {
			accId := section.Key("sso_account_id").Value()
			roleName := section.Key("sso_role_name").Value()
			credsInput := sso.GetRoleCredentialsInput{
				AccessToken: s.accessToken,
				AccountId:   &accId,
				RoleName:    &roleName,
			}
			wg.Add(1)
			go s.getRoleCreds(&credsInput, &wg, queue, section.Name())
		}
	}
	wg.Wait()
	close(queue)
	for item := range queue {
		if item.err != nil {
			result = append(result, CredentialsResult{
				ProfileName:  item.roleName,
				WasSuccesful: false,
			})
			continue
		}
		profName := fmt.Sprintf(
			"%s:%s",
			tempCredsPrefix,
			strings.TrimPrefix(item.roleName, "profile "),
		)
		credsSection, err := creds.File.NewSection(profName)
		if err != nil {
			return nil, item.err
		}
		expiresTime := *item.creds.RoleCredentials.Expiration / 1000
		credsSection.NewKey("aws_access_key_id", *item.creds.RoleCredentials.AccessKeyId)
		credsSection.NewKey("aws_secret_access_key", *item.creds.RoleCredentials.SecretAccessKey)
		credsSection.NewKey("aws_session_token", *item.creds.RoleCredentials.SessionToken)
		credsSection.NewKey("issued_time", fmt.Sprint(time.Now().Unix()))
		credsSection.NewKey("expires_time", fmt.Sprint(expiresTime))
		credsSection.NewKey("org", s.orgName)
		credsSection.NewKey("sso_auto_populated", "true")

		result = append(result, CredentialsResult{
			ProfileName:  profName,
			WasSuccesful: true,
			ExpiresAt:    fmt.Sprint(time.Unix(expiresTime, 0).Local()),
		})
	}

	return result, creds.Save()
}

func (s *SSOFlow) getRoleCreds(
	input *sso.GetRoleCredentialsInput,
	wg *sync.WaitGroup,
	channel chan RoleCredentialsOutput,
	roleName string,
) {
	var result RoleCredentialsOutput
	result.roleName = roleName
	credsOutput, err := s.ssoClient.GetRoleCredentials(input)
	if err != nil {
		result.err = err
	}
	result.creds = credsOutput
	channel <- result
	if wg != nil {
		wg.Done()
	}
}

func (s *SSOFlow) GetCredsByRoleName(roleName string, accountId string) (*sso.GetRoleCredentialsOutput, error) {
	var result RoleCredentialsOutput
	result.roleName = roleName
	credsOutput, err := s.ssoClient.GetRoleCredentials(&sso.GetRoleCredentialsInput{
		AccessToken: s.accessToken,
		AccountId:   &accountId,
		RoleName:    &roleName,
	})
	if err != nil {
		return nil, err
	}
	return credsOutput, nil
}

func GetCachedSSOFlow(org config.Organization) (*SSOFlow, error) {
	session := session.Must(session.NewSession())
	ssoClient := ssooidc.New(session, aws.NewConfig().WithRegion(org.Region))
	clientCredentials, err := cache.GetSSOClientCreds(org.Region)
	if err != nil {
		return nil, err
	}

	if clientCredentials == nil {
		return nil, fmt.Errorf("Unable to get client credentials, please login with this CLI and then try again")
	}

	var ssoToken *cache.SSOToken
	ssoToken, err = cache.GetSSOToken(org.URL, session, ssoClient, org.Region)
	if err != nil {
		return nil, err
	}

	if ssoToken == nil {
		return nil, fmt.Errorf("Unable to get sso token, please login with this CLI and then try again")
	}
	ssoServiceClient := sso.New(session, aws.NewConfig().WithRegion(org.Region))
	file, err := files.NewConfigFile(config.GetInstance().Home)
	if err != nil {
		return nil, err
	}

	return &SSOFlow{
		accessToken: &ssoToken.AccessToken,
		ssoClient:   ssoServiceClient,
		configFile:  file,
		ssoRegion:   &org.Region,
		ssoStartUrl: &org.URL,
		orgName:     org.Name,
		prefix:      org.Prefix,
	}, nil
}
