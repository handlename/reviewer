test:
	go tool gotestsum --format testdox

ci:
	go tool gotestsum --format testdox --junitfile test-report.xml
