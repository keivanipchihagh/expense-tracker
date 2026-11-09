.PHONY: test lint mocks deploy

COMPOSE = docker compose -f deploy/docker-compose.yaml --env-file .env
HEALTH_URL = http://localhost:$${HOST_PORT:-8000}/health

test:
	bash ./scripts/test.sh

lint:
	bash ./scripts/lint.sh

mocks:
	bash ./scripts/mocks.sh

# Rebuild the image, restart the container with .env, and wait up to 30s
# for /health to answer.
deploy:
	$(COMPOSE) up -d --build
	@for i in $$(seq 30); do \
		if curl -fsS -o /dev/null $(HEALTH_URL); then echo "Deployed and healthy"; exit 0; fi; \
		sleep 1; \
	done; \
	echo "Not healthy after 30s. Logs: $(COMPOSE) logs app"; exit 1
