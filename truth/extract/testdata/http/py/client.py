import requests
import httpx


def go(session):
    requests.get("https://py.example.com/a")
    httpx.post('https://httpx.example.com/b', json={})
    session.get(f"https://sess.example.com/c/{1}")
    requests.get("http://localhost:8000/x")
    requests.get(url)
    # requests.get("https://comment.example.com")
    """
    requests.get("https://docstring.example.com")
    """
