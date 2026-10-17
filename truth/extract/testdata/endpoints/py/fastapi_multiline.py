from fastapi import APIRouter

router = APIRouter(prefix="/v")


@router.get(
    "/multi-line",
    tags=["x"],
)
def f(): ...
