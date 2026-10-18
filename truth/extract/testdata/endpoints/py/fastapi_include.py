from fastapi import APIRouter, FastAPI

app = FastAPI()
orders = APIRouter(prefix="/orders")
bare = APIRouter()
twice = APIRouter(prefix="/t")


@orders.get("/{id}")
def get_order(): ...


@bare.get("/ping")
def ping(): ...


@twice.get("/x")
def x(): ...


app.include_router(orders, prefix="/api/v1")
app.include_router(bare, prefix="/internal")
app.include_router(other.router, prefix="/ignored")
app.include_router(twice, prefix="/a")
app.include_router(twice, prefix="/b")
