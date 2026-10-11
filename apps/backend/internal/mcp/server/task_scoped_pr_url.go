package mcp

import "github.com/kandev/kandev/internal/common/securityutil"

func validateScopedPRURLArguments(args map[string]interface{}) string {
	rawURL, hasURL := args["pr_url"]
	rawHead, hasHead := args["expected_head_sha"]
	if !hasURL && !hasHead {
		return ""
	}
	url, urlOK := rawURL.(string)
	head, headOK := rawHead.(string)
	if !hasURL || !hasHead || !urlOK || url == "" || !headOK || len(head) != 40 || !securityutil.LooksLikeCommitSHA(head) || args["operation"] != "link" || args["provider"] != "github" {
		return "pr_url and a full expected_head_sha require a GitHub link operation"
	}
	return ""
}
