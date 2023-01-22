lint:
	golangci-lint run ./... -v

cover:
	@go test $(go list ./... | grep -v /mocks/) -v -covermode=count -coverprofile=coverage.out
	@goveralls -coverprofile=coverage.out -service=travis-ci -repotoken 5wBSadt931gVD47D5zoKybmuiArrY5Yq8
