package validation

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/pkg/config"
	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	"github.com/kyverno/kyverno/pkg/engine/handlers"
	"github.com/kyverno/kyverno/pkg/engine/internal"
	engineutils "github.com/kyverno/kyverno/pkg/engine/utils"
	apiutils "github.com/kyverno/kyverno/pkg/utils/api"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type validateImageHandler struct {
	configuration config.Configuration
}

func NewValidateImageHandler(
	configuration config.Configuration,
) handlers.Handler {
	return validateImageHandler{
		configuration: configuration,
	}
}

func (h validateImageHandler) Process(
	ctx context.Context,
	logger logr.Logger,
	policyContext engineapi.PolicyContext,
	resource unstructured.Unstructured,
	rule kyvernov1.Rule,
) (unstructured.Unstructured, []engineapi.RuleResponse) {
	if engineutils.IsDeleteRequest(policyContext) {
		return resource, nil
	}
	matchingImages, _, err := engineutils.ExtractMatchingImages(
		policyContext.NewResource(),
		policyContext.JSONContext(),
		rule,
		h.configuration,
	)
	if err != nil {
		return resource, handlers.RuleResponses(internal.RuleError(&rule, engineapi.Validation, "", err))
	}
	if len(matchingImages) == 0 {
		return resource, handlers.RuleResponses(internal.RuleSkip(&rule, engineapi.Validation, "image verified"))
	}
	preconditionsPassed, err := internal.CheckPreconditions(logger, policyContext, rule.RawAnyAllConditions)
	if err != nil {
		return resource, handlers.RuleResponses(internal.RuleError(&rule, engineapi.Validation, "failed to evaluate preconditions", err))
	}
	if !preconditionsPassed {
		if policyContext.Policy().GetSpec().ValidationFailureAction.Audit() {
			return resource, nil
		}

		return resource, handlers.RuleResponses(internal.RuleSkip(&rule, engineapi.Validation, "preconditions not met"))
	}
	for _, v := range rule.VerifyImages {
		imageVerify := v.Convert()
		for _, infoMap := range policyContext.JSONContext().ImageInfo() {
			for name, imageInfo := range infoMap {
				image := imageInfo.String()

				if !engineutils.ImageMatches(image, imageVerify.ImageReferences) {
					logger.V(4).Info("image does not match", "imageReferences", imageVerify.ImageReferences)
					return resource, nil
				}

				logger.V(4).Info("validating image", "image", image)
				if err := validateImage(policyContext, imageVerify, name, imageInfo, logger); err != nil {
					return resource, handlers.RuleResponses(internal.RuleResponse(rule, engineapi.ImageVerify, err.Error(), engineapi.RuleStatusFail))
				}
			}
		}
	}
	logger.V(4).Info("validated image", "rule", rule.Name)
	return resource, handlers.RuleResponses(internal.RulePass(&rule, engineapi.Validation, "image verified"))
}

func validateImage(ctx engineapi.PolicyContext, imageVerify *kyvernov1.ImageVerification, name string, imageInfo apiutils.ImageInfo, log logr.Logger) error {
	image := imageInfo.String()
	if imageVerify.VerifyDigest && imageInfo.Digest == "" {
		log.V(2).Info("missing digest", "image", imageInfo.String())
		return fmt.Errorf("missing digest for %s", image)
	}
	newResource := ctx.NewResource()
	if imageVerify.Required && newResource.Object != nil {
		verified, err := engineutils.IsImageVerified(newResource, image, log)
		if err != nil {
			return err
		}
		if !verified {
			return fmt.Errorf("unverified image %s", image)
		}
	}
	return nil
}