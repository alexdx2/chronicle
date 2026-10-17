from aws_cdk import aws_sqs as sqs, aws_sns as sns

sqs.Queue(self, "Q", queue_name="cdk-py-queue")
sns.Topic(self, "T", topic_name="cdk-py-topic")
sqs.Queue(self, "C", queue_name="q-" + env)
