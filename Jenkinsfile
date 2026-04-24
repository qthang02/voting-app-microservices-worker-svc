pipeline {
    agent {
        kubernetes {
            yaml '''
                apiVersion: v1
                kind: Pod
                spec:
                  containers:
                    - name: buildctl
                      image: moby/buildkit:latest
                      command: ["sleep"]
                      args: ["infinity"]
                      env:
                        - name: BUILDKIT_HOST
                          value: "tcp://buildkitd.jenkins.svc.cluster.local:1234"
            '''
            defaultContainer 'buildctl'
        }
    }

    environment {
        IMAGE_REGISTRY = 'thang909/voting-app-microservices-worker-svc'
        BUILDKIT_HOST  = "tcp://buildkitd.jenkins.svc.cluster.local:1234"
    }

    stages {
        stage('Checkout') {
            steps {
                git branch: 'develop',
                    url: 'https://github.com/qthang02/voting-app-microservices-worker-svc'
            }
        }

        stage('Build & Push') {
            steps {
                withCredentials([usernamePassword(
                    credentialsId: 'docker-registry-creds',
                    usernameVariable: 'DOCKER_USER',
                    passwordVariable: 'DOCKER_PASS'
                )]) {
                    sh '''
                        mkdir -p /root/.docker
                        AUTH=$(echo -n "$DOCKER_USER:$DOCKER_PASS" | base64)
                        cat > /root/.docker/config.json <<EOF
{
    "auths": {
        "https://index.docker.io/v1/": {
            "auth": "$AUTH"
        }
    }
}
EOF

                        buildctl \
                          --addr ${BUILDKIT_HOST} \
                          build \
                          --frontend=dockerfile.v0 \
                          --local context=. \
                          --local dockerfile=. \
                          --output type=image,name=${IMAGE_REGISTRY}:${BUILD_NUMBER},push=true \
                          --export-cache type=registry,ref=${IMAGE_REGISTRY}:buildcache,mode=min \
                          --import-cache type=registry,ref=${IMAGE_REGISTRY}:buildcache
                    '''
                }
            }
        }
    }

    post {
        always {
            sh 'rm -f /root/.docker/config.json || true'
        }
    }
}