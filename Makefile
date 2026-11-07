.PHONY: test lint mocks

test:
	bash ./scripts/test.sh

lint:
	bash ./scripts/lint.sh

mocks:
	bash ./scripts/mocks.sh
