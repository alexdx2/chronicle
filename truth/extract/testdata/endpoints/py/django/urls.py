from django.urls import path, re_path, include

urlpatterns = [
    path("", views.index),
    path("orders/<int:pk>/", views.order),
    re_path(r"^legacy/(?P<id>\d+)/$", views.legacy),
    re_path(r"^about/$", views.about),
    re_path(r"^(a|b)/$", views.alt),
    path("api/", include("api.urls")),
    # path("hidden/", views.h),
]
