from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("orders", __name__, url_prefix="/orders")


@app.route("/")
def index(): ...


@app.route("/health", methods=["GET"])
def health(): ...


@bp.route("/<int:order_id>", methods=["POST", "PUT"])
def order(order_id): ...


@bp.route("/list")
def lst(): ...


# @app.route("/hidden")
