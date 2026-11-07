package main

import (
	"github.com/keivanipchihagh/expense-tracker/internal"
	"github.com/keivanipchihagh/expense-tracker/pkg/service"
)

func main() {
	service.Run("expense-tracker", &internal.Service{})
}
