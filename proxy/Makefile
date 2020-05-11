ifdef REPO
	REPO=$(shell echo $${REPO:+$${REPO%%/*}/})
endif

all: proxy

proxy: main.go
	docker build -t $(REPO)proxy .

ifdef REPO
	docker push $(REPO)proxy
endif

deploy: all
	sed "s|REPO/|$(REPO)|" < proxy.yaml | kubectl apply -f -

undeploy: 
	-sed "s|REPO/|$(REPO)|" < proxy.yaml | kubectl delete -f -