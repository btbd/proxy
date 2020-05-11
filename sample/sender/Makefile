ifdef REPO
	REPO=$(shell echo $${REPO:+$${REPO%%/*}/})
endif

all: sender

sender: main.go
	docker build -t $(REPO)sender .

ifdef REPO
	docker push $(REPO)sender
endif

deploy: all
	sed "s|REPO/|$(REPO)|" < sender.yaml | kubectl apply -f -

undeploy: 
	-sed "s|REPO/|$(REPO)|" < sender.yaml | kubectl delete -f -