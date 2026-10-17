from aws_cdk import aws_lambda as lambda_, Stack

class S(Stack):
    def __init__(self, scope, id):
        super().__init__(scope, id)
        lambda_.Function(self, "PyFn",
            function_name="py-worker",
            runtime=lambda_.Runtime.PYTHON_3_11)
        lambda_.Function(self, "OtherFn", handler="x.y")
