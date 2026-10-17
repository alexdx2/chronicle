from fastapi import APIRouter, FastAPI

router = APIRouter(prefix="/users", tags=["users"])
admin = APIRouter(
    prefix="/admin",
)
app = FastAPI()


@router.get("/")
def list_users(): ...


@router.get("/{user_id}")
def get_user(user_id: int): ...


@router.post("")
def create_user(): ...


# @router.delete("/hidden")
@app.get("/health")
def health(): ...


@router.api_route("/multi", methods=["GET", "POST"])
def multi(): ...


@admin.delete("/{id}")
def drop(id: int): ...
