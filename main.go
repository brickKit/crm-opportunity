// Command crm-opportunity runs the opportunity component alone (serve, migrate); the shells compile in
// opportunity.Definition().
package main

import (
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/crm-opportunity/v3/opportunity"
)

func main() { launcher.Main(opportunity.Definition()) }
