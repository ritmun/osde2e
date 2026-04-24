package aws

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/openshift/osde2e/pkg/common/config"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
)

var (
	rolesubstr     = "osde2e"
	providersubstr = "cloudfront"
)

// isOIDCProviderFromActiveCluster checks if an OIDC provider belongs to an active cluster
// Returns true if the provider should be skipped (belongs to active cluster), false if it can be cleaned up
func isOIDCProviderFromActiveCluster(url string, activeClusters map[string]bool) bool {
	// Extract cluster name from OIDC URL
	// Example: "osde2e-i5u38-oidc-t8i8.s3.us-west-2.amazonaws.com" -> "osde2e-i5u38"
	re := regexp.MustCompile(`^(osde2e-[^-]+)-oidc-`)
	matches := re.FindStringSubmatch(url)
	if len(matches) >= 2 {
		clusterName := matches[1]
		if activeClusters[clusterName] {
			log.Printf("Skipping OIDC provider for active cluster %s: %s\n", clusterName, url)
			return true
		}
	}
	return false
}

// isRoleFromActiveCluster checks if an IAM role belongs to an active cluster
// Returns true if the role should be skipped (belongs to active cluster), false if it can be cleaned up
func isRoleFromActiveCluster(roleArn string, activeClusters map[string]bool) bool {
	// Extract cluster name from role ARN
	// Example: "arn:aws:iam::123456789012:role/osde2e-i5u38-installer-role" -> "osde2e-i5u38"
	re := regexp.MustCompile(`osde2e-[^-]+-`)
	matches := re.FindStringSubmatch(roleArn)
	if len(matches) >= 1 {
		// Remove the trailing dash to get the cluster name
		clusterName := strings.TrimSuffix(matches[0], "-")
		if activeClusters[clusterName] {
			log.Printf("Skipping IAM role for active cluster %s: %s\n", clusterName, roleArn)
			return true
		}
	}
	return false
}

func (CcsAwsSession *ccsAwsSession) CleanupOpenIDConnectProviders(activeClusters map[string]bool, dryrun bool, sendSummary bool,
	errorBuilder *strings.Builder,
) (counters Counters, err error) {
	err = CcsAwsSession.GetAWSSessions()
	if err != nil {
		return counters, err
	}

	input := &iam.ListOpenIDConnectProvidersInput{}
	result, err := CcsAwsSession.iam.ListOpenIDConnectProviders(input)
	if err != nil {
		return counters, err
	}

	recordOidcFailure := func(arn, detail string) {
		counters.Failed++
		msg := fmt.Sprintf("OIDC provider %s: %s\n", arn, detail)
		fmt.Print(msg)
		if sendSummary && errorBuilder.Len() < config.SlackMessageLength {
			errorBuilder.WriteString(msg)
		}
	}

	for _, provider := range result.OpenIDConnectProviderList {
		arn := aws.StringValue(provider.Arn)
		if arn == "" {
			continue
		}

		output, errGet := CcsAwsSession.iam.GetOpenIDConnectProvider(&iam.GetOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: provider.Arn,
		})
		if errGet != nil {
			recordOidcFailure(arn, fmt.Sprintf("get provider: %v", errGet))
			continue
		}
		if output.Url == nil {
			continue
		}
		url := aws.StringValue(output.Url)

		// If provider url contains "cloudfront" or "osde2e-", delete it
		if !strings.Contains(url, providersubstr) && !strings.Contains(url, rolesubstr) || isOIDCProviderFromActiveCluster(url, activeClusters) {
			continue
		}

		fmt.Printf("Provider will be deleted: %s (URL: %s)\n", arn, url)

		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteOpenIDConnectProvider(&iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if errDel != nil {
				recordOidcFailure(arn, fmt.Sprintf("not deleted: %v", errDel))
				continue
			}
			counters.Deleted++
			fmt.Println("Deleted")
		}
	}

	return counters, nil
}

// removeRoleFromAllInstanceProfiles lists instance profiles for the role, then removes
// the role from each. Returns nil on success; on failure the error message is suitable
// for role cleanup reporting (no role name prefix).
func (CcsAwsSession *ccsAwsSession) removeRoleFromAllInstanceProfiles(role *iam.Role, dryrun bool) error {
	instanceProfiles, err := CcsAwsSession.iam.ListInstanceProfilesForRole(
		&iam.ListInstanceProfilesForRoleInput{RoleName: role.RoleName},
	)
	if err != nil {
		return fmt.Errorf("list instance profiles: %w", err)
	}

	var errs []string
	for _, instanceProfile := range instanceProfiles.InstanceProfiles {
		if instanceProfile.InstanceProfileName == nil {
			continue
		}
		ipn := aws.StringValue(instanceProfile.InstanceProfileName)
		fmt.Println("Removing role from instance profile: ", ipn)
		if !dryrun {
			_, errRm := CcsAwsSession.iam.RemoveRoleFromInstanceProfile(&iam.RemoveRoleFromInstanceProfileInput{
				InstanceProfileName: instanceProfile.InstanceProfileName,
				RoleName:            role.RoleName,
			})
			if errRm != nil {
				errs = append(errs, fmt.Sprintf("profile %s: %v", ipn, errRm))
			} else {
				fmt.Println("Removed")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("instance profile removal: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteAllInlineRolePolicies lists and deletes every inline policy on the role.
func (CcsAwsSession *ccsAwsSession) deleteAllInlineRolePolicies(role *iam.Role, dryrun bool) error {
	inlinePolicies, err := CcsAwsSession.iam.ListRolePolicies(&iam.ListRolePoliciesInput{
		RoleName: role.RoleName,
	})
	if err != nil {
		return fmt.Errorf("list inline policies: %w", err)
	}

	var errs []string
	for _, policy := range inlinePolicies.PolicyNames {
		pn := aws.StringValue(policy)
		fmt.Println("Inline policy will be deleted: ", pn)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
				PolicyName: policy,
				RoleName:   role.RoleName,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("policy %s: %v", pn, errDel))
			} else {
				fmt.Println("Deleted")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete inline policies: %s", strings.Join(errs, "; "))
	}
	return nil
}

// detachAllAttachedRolePolicies lists and detaches every policy attached to the role (managed policies;
// inline policies are removed in deleteAllInlineRolePolicies).
func (CcsAwsSession *ccsAwsSession) detachAllAttachedRolePolicies(role *iam.Role, dryrun bool) error {
	attachedPolicies, err := CcsAwsSession.iam.ListAttachedRolePolicies(&iam.ListAttachedRolePoliciesInput{
		RoleName: role.RoleName,
	})
	if err != nil {
		return fmt.Errorf("list attached policies: %w", err)
	}

	var errs []string
	for _, policy := range attachedPolicies.AttachedPolicies {
		if policy.PolicyName == nil || policy.PolicyArn == nil {
			continue
		}
		polName := aws.StringValue(policy.PolicyName)
		fmt.Println("Policy will be detached: ", polName)
		if !dryrun {
			_, errDetach := CcsAwsSession.iam.DetachRolePolicy(&iam.DetachRolePolicyInput{
				PolicyArn: policy.PolicyArn,
				RoleName:  role.RoleName,
			})
			if errDetach != nil {
				errs = append(errs, fmt.Sprintf("policy %s: %v", polName, errDetach))
			} else {
				time.Sleep(2 * time.Second)
				fmt.Println("Detached")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("detach managed policies: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteIAMRole calls IAM DeleteRole for the given role.
func (CcsAwsSession *ccsAwsSession) deleteIAMRole(role *iam.Role) error {
	_, err := CcsAwsSession.iam.DeleteRole(&iam.DeleteRoleInput{RoleName: role.RoleName})
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	return nil
}

// cleanupOsde2eRole removes one osde2e IAM role: instance profiles, inline and
// attached policies, then DeleteRole. At most one Failed increment per role;
// Deleted increments only after a successful DeleteRole when not dry-run.
func (CcsAwsSession *ccsAwsSession) cleanupOsde2eRole(
	role *iam.Role,
	roleName string,
	dryrun bool,
	sendSummary bool,
	errorBuilder *strings.Builder,
	counters *Counters,
) {
	recordRoleFailure := func(detail string) {
		counters.Failed++
		msg := fmt.Sprintf("role %s: %s\n", roleName, detail)
		fmt.Print(msg)
		if sendSummary && errorBuilder.Len() < config.SlackMessageLength {
			errorBuilder.WriteString(msg)
		}
	}

	fmt.Printf("Role will be deleted: %s\n", roleName)

	if err := CcsAwsSession.removeRoleFromAllInstanceProfiles(role, dryrun); err != nil {
		recordRoleFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteAllInlineRolePolicies(role, dryrun); err != nil {
		recordRoleFailure(err.Error())
		return
	}
	if err := CcsAwsSession.detachAllAttachedRolePolicies(role, dryrun); err != nil {
		recordRoleFailure(err.Error())
		return
	}
	if !dryrun {
		if err := CcsAwsSession.deleteIAMRole(role); err != nil {
			recordRoleFailure(err.Error())
			return
		}
		fmt.Println("Deleted role", roleName)
		counters.Deleted++
	}
}

func (CcsAwsSession *ccsAwsSession) CleanupRoles(activeClusters map[string]bool, dryrun bool, sendSummary bool,
	errorBuilder *strings.Builder,
) (counters Counters, err error) {
	err = CcsAwsSession.GetAWSSessions()
	if err != nil {
		return counters, err
	}

	input := &iam.ListRolesInput{
		MaxItems: aws.Int64(1000),
	}
	result, err := CcsAwsSession.iam.ListRoles(input)
	if err != nil {
		return counters, err
	}

	for _, role := range result.Roles {
		if role.RoleName == nil || role.Arn == nil {
			continue
		}
		roleName := aws.StringValue(role.RoleName)
		if !strings.Contains(*role.Arn, rolesubstr) || isRoleFromActiveCluster(*role.Arn, activeClusters) {
			continue
		}
		CcsAwsSession.cleanupOsde2eRole(role, roleName, dryrun, sendSummary, errorBuilder, &counters)
	}

	return counters, nil
}

// deleteUserAccessKeys lists and deletes all access keys for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserAccessKeys(userName *string, dryrun bool) error {
	accessKeys, err := CcsAwsSession.iam.ListAccessKeys(&iam.ListAccessKeysInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list access keys: %w", err)
	}

	var errs []string
	for _, key := range accessKeys.AccessKeyMetadata {
		if key.AccessKeyId == nil {
			continue
		}
		keyID := aws.StringValue(key.AccessKeyId)
		fmt.Printf("  Access key will be deleted: %s\n", keyID)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteAccessKey(&iam.DeleteAccessKeyInput{
				UserName:    userName,
				AccessKeyId: key.AccessKeyId,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("key %s: %v", keyID, errDel))
			} else {
				fmt.Println("  Deleted access key")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete access keys: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserInlinePolicies lists and deletes all inline policies for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserInlinePolicies(userName *string, dryrun bool) error {
	policies, err := CcsAwsSession.iam.ListUserPolicies(&iam.ListUserPoliciesInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list inline policies: %w", err)
	}

	var errs []string
	for _, policy := range policies.PolicyNames {
		policyName := aws.StringValue(policy)
		fmt.Printf("  Inline policy will be deleted: %s\n", policyName)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteUserPolicy(&iam.DeleteUserPolicyInput{
				UserName:   userName,
				PolicyName: policy,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("policy %s: %v", policyName, errDel))
			} else {
				fmt.Println("  Deleted inline policy")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete inline policies: %s", strings.Join(errs, "; "))
	}
	return nil
}

// detachUserManagedPolicies lists and detaches all managed policies from a user.
func (CcsAwsSession *ccsAwsSession) detachUserManagedPolicies(userName *string, dryrun bool) error {
	policies, err := CcsAwsSession.iam.ListAttachedUserPolicies(&iam.ListAttachedUserPoliciesInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list attached policies: %w", err)
	}

	var errs []string
	for _, policy := range policies.AttachedPolicies {
		if policy.PolicyArn == nil || policy.PolicyName == nil {
			continue
		}
		policyName := aws.StringValue(policy.PolicyName)
		fmt.Printf("  Managed policy will be detached: %s\n", policyName)
		if !dryrun {
			_, errDetach := CcsAwsSession.iam.DetachUserPolicy(&iam.DetachUserPolicyInput{
				UserName:  userName,
				PolicyArn: policy.PolicyArn,
			})
			if errDetach != nil {
				errs = append(errs, fmt.Sprintf("policy %s: %v", policyName, errDetach))
			} else {
				time.Sleep(1 * time.Second)
				fmt.Println("  Detached managed policy")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("detach managed policies: %s", strings.Join(errs, "; "))
	}
	return nil
}

// removeUserFromAllGroups lists and removes user from all groups.
func (CcsAwsSession *ccsAwsSession) removeUserFromAllGroups(userName *string, dryrun bool) error {
	groups, err := CcsAwsSession.iam.ListGroupsForUser(&iam.ListGroupsForUserInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	var errs []string
	for _, group := range groups.Groups {
		if group.GroupName == nil {
			continue
		}
		groupName := aws.StringValue(group.GroupName)
		fmt.Printf("  Removing from group: %s\n", groupName)
		if !dryrun {
			_, errRm := CcsAwsSession.iam.RemoveUserFromGroup(&iam.RemoveUserFromGroupInput{
				UserName:  userName,
				GroupName: group.GroupName,
			})
			if errRm != nil {
				errs = append(errs, fmt.Sprintf("group %s: %v", groupName, errRm))
			} else {
				fmt.Println("  Removed from group")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove from groups: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserMFADevices lists and deletes all MFA devices for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserMFADevices(userName *string, dryrun bool) error {
	// Delete virtual MFA devices
	virtualMFADevices, err := CcsAwsSession.iam.ListMFADevices(&iam.ListMFADevicesInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list MFA devices: %w", err)
	}

	var errs []string
	for _, device := range virtualMFADevices.MFADevices {
		if device.SerialNumber == nil {
			continue
		}
		serialNumber := aws.StringValue(device.SerialNumber)
		fmt.Printf("  MFA device will be deactivated: %s\n", serialNumber)
		if !dryrun {
			_, errDeactivate := CcsAwsSession.iam.DeactivateMFADevice(&iam.DeactivateMFADeviceInput{
				UserName:     userName,
				SerialNumber: device.SerialNumber,
			})
			if errDeactivate != nil {
				errs = append(errs, fmt.Sprintf("deactivate %s: %v", serialNumber, errDeactivate))
			} else {
				fmt.Println("  Deactivated MFA device")
				// Delete virtual MFA device if it's a virtual device (contains "mfa" in path)
				if strings.Contains(serialNumber, "mfa") {
					_, errDel := CcsAwsSession.iam.DeleteVirtualMFADevice(&iam.DeleteVirtualMFADeviceInput{
						SerialNumber: device.SerialNumber,
					})
					if errDel != nil {
						errs = append(errs, fmt.Sprintf("delete virtual MFA %s: %v", serialNumber, errDel))
					} else {
						fmt.Println("  Deleted virtual MFA device")
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete MFA devices: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserSigningCertificates lists and deletes all signing certificates for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserSigningCertificates(userName *string, dryrun bool) error {
	certificates, err := CcsAwsSession.iam.ListSigningCertificates(&iam.ListSigningCertificatesInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list signing certificates: %w", err)
	}

	var errs []string
	for _, cert := range certificates.Certificates {
		if cert.CertificateId == nil {
			continue
		}
		certID := aws.StringValue(cert.CertificateId)
		fmt.Printf("  Signing certificate will be deleted: %s\n", certID)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteSigningCertificate(&iam.DeleteSigningCertificateInput{
				UserName:      userName,
				CertificateId: cert.CertificateId,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("cert %s: %v", certID, errDel))
			} else {
				fmt.Println("  Deleted signing certificate")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete signing certificates: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserSSHPublicKeys lists and deletes all SSH public keys for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserSSHPublicKeys(userName *string, dryrun bool) error {
	sshKeys, err := CcsAwsSession.iam.ListSSHPublicKeys(&iam.ListSSHPublicKeysInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list SSH public keys: %w", err)
	}

	var errs []string
	for _, key := range sshKeys.SSHPublicKeys {
		if key.SSHPublicKeyId == nil {
			continue
		}
		keyID := aws.StringValue(key.SSHPublicKeyId)
		fmt.Printf("  SSH public key will be deleted: %s\n", keyID)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteSSHPublicKey(&iam.DeleteSSHPublicKeyInput{
				UserName:       userName,
				SSHPublicKeyId: key.SSHPublicKeyId,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("key %s: %v", keyID, errDel))
			} else {
				fmt.Println("  Deleted SSH public key")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete SSH public keys: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserServiceSpecificCredentials lists and deletes all service-specific credentials for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserServiceSpecificCredentials(userName *string, dryrun bool) error {
	creds, err := CcsAwsSession.iam.ListServiceSpecificCredentials(&iam.ListServiceSpecificCredentialsInput{
		UserName: userName,
	})
	if err != nil {
		return fmt.Errorf("list service-specific credentials: %w", err)
	}

	var errs []string
	for _, cred := range creds.ServiceSpecificCredentials {
		if cred.ServiceSpecificCredentialId == nil {
			continue
		}
		credID := aws.StringValue(cred.ServiceSpecificCredentialId)
		fmt.Printf("  Service-specific credential will be deleted: %s\n", credID)
		if !dryrun {
			_, errDel := CcsAwsSession.iam.DeleteServiceSpecificCredential(&iam.DeleteServiceSpecificCredentialInput{
				UserName:                    userName,
				ServiceSpecificCredentialId: cred.ServiceSpecificCredentialId,
			})
			if errDel != nil {
				errs = append(errs, fmt.Sprintf("credential %s: %v", credID, errDel))
			} else {
				fmt.Println("  Deleted service-specific credential")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete service-specific credentials: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteUserLoginProfile deletes the login profile (console password) for a user.
func (CcsAwsSession *ccsAwsSession) deleteUserLoginProfile(userName *string, dryrun bool) error {
	// First check if the login profile exists
	_, err := CcsAwsSession.iam.GetLoginProfile(&iam.GetLoginProfileInput{
		UserName: userName,
	})
	if err != nil {
		// If NoSuchEntity error, login profile doesn't exist, which is fine
		if strings.Contains(err.Error(), "NoSuchEntity") {
			return nil
		}
		return fmt.Errorf("get login profile: %w", err)
	}

	fmt.Printf("  Login profile will be deleted\n")
	if !dryrun {
		_, errDel := CcsAwsSession.iam.DeleteLoginProfile(&iam.DeleteLoginProfileInput{
			UserName: userName,
		})
		if errDel != nil {
			return fmt.Errorf("delete login profile: %v", errDel)
		}
		fmt.Println("  Deleted login profile")
	}
	return nil
}

// cleanupIAMUser removes one IAM user with all dependencies.
func (CcsAwsSession *ccsAwsSession) cleanupIAMUser(
	user *iam.User,
	userName string,
	dryrun bool,
	sendSummary bool,
	errorBuilder *strings.Builder,
	counters *Counters,
) {
	recordUserFailure := func(detail string) {
		counters.Failed++
		msg := fmt.Sprintf("user %s: %s\n", userName, detail)
		fmt.Print(msg)
		if sendSummary && errorBuilder.Len() < config.SlackMessageLength {
			errorBuilder.WriteString(msg)
		}
	}

	fmt.Printf("User will be deleted: %s\n", userName)

	// Delete all dependencies in order
	if err := CcsAwsSession.deleteUserAccessKeys(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserInlinePolicies(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.detachUserManagedPolicies(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.removeUserFromAllGroups(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserMFADevices(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserSigningCertificates(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserSSHPublicKeys(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserServiceSpecificCredentials(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}
	if err := CcsAwsSession.deleteUserLoginProfile(user.UserName, dryrun); err != nil {
		recordUserFailure(err.Error())
		return
	}

	// Finally delete the user
	if !dryrun {
		_, err := CcsAwsSession.iam.DeleteUser(&iam.DeleteUserInput{
			UserName: user.UserName,
		})
		if err != nil {
			recordUserFailure(fmt.Sprintf("delete user: %v", err))
			return
		}
		fmt.Printf("Deleted user %s\n", userName)
		counters.Deleted++
	}
}

// CleanupAllUsers deletes all IAM users except the excluded user (typically osdCcsAdmin).
func (CcsAwsSession *ccsAwsSession) CleanupAllUsers(
	excludeUser string,
	dryrun bool,
	sendSummary bool,
	errorBuilder *strings.Builder,
) (counters Counters, err error) {
	err = CcsAwsSession.GetAWSSessions()
	if err != nil {
		return counters, err
	}

	fmt.Printf("Listing all IAM users (excluding: %s)...\n", excludeUser)

	input := &iam.ListUsersInput{
		MaxItems: aws.Int64(1000),
	}
	result, err := CcsAwsSession.iam.ListUsers(input)
	if err != nil {
		return counters, fmt.Errorf("list users: %w", err)
	}

	for _, user := range result.Users {
		if user.UserName == nil {
			continue
		}
		userName := aws.StringValue(user.UserName)

		// Skip the excluded user
		if userName == excludeUser {
			fmt.Printf("Skipping excluded user: %s\n", userName)
			continue
		}

		CcsAwsSession.cleanupIAMUser(user, userName, dryrun, sendSummary, errorBuilder, &counters)
	}

	return counters, nil
}
