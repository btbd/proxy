all:
	cd ../proxy && make
	cd recipient && make
	cd sender && make 

deploy:
	cd ../proxy && make deploy
	cd recipient && make deploy
	cd sender && make deploy

undeploy:
	-cd ../proxy && make undeploy
	-cd recipient && make undeploy
	-cd sender && make undeploy 