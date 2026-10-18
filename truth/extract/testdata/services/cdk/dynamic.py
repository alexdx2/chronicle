from aws_cdk import aws_lambda as lambda_, Stack

class D(Stack):
    def __init__(self, scope, id, stage):
        super().__init__(scope, id)
        lambda_.Function(self, "Concat",
            function_name="fn-" + stage)
        lambda_.Function(self, "Lowered",
            function_name="Up".lower())
        for i in range(3):
            lambda_.Function(self, "C" + str(i), handler="x.y")
