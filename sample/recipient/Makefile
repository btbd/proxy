ifdef REPO
	REPO=$(shell echo $${REPO:+$${REPO%%/*}/})
endif

all: recipient

recipient: main.go
	docker build -t $(REPO)recipient .

ifdef REPO
	docker push $(REPO)recipient
endif

deploy: all
	sed "s|REPO/|$(REPO)|" < recipient.yaml | kubectl apply -f -

undeploy: 
	-sed "s|REPO/|$(REPO)|" < recipient.yaml | kubectl delete -f -