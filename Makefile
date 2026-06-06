.PHONY: lint

lint:
	golangci-lint run $$(go list -f '{{.Dir}}/...' -m | xargs)

lint-fix:
	golangci-lint run --fix $$(go list -f '{{.Dir}}/...' -m | xargs)
