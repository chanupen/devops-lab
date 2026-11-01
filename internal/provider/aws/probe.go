package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	budgettypes "github.com/aws/aws-sdk-go-v2/service/budgets/types"
)

// BudgetName is the baseline's monthly budget.
const BudgetName = "lab-monthly"

// BucketExists reports whether the account's state bucket exists.
func BucketExists(ctx context.Context, cfg aws.Config, account string) (bool, error) {
	return (&Baseline{Config: cfg, Account: account}).Remote(ctx)
}

// BudgetExists reports whether the baseline's budget exists.
func BudgetExists(ctx context.Context, cfg aws.Config, account string) (bool, error) {
	_, err := budgets.NewFromConfig(cfg).DescribeBudget(ctx, &budgets.DescribeBudgetInput{
		AccountId: aws.String(account), BudgetName: aws.String(BudgetName)})
	var nf *budgettypes.NotFoundException
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &nf):
		return false, nil
	}
	return false, err
}
