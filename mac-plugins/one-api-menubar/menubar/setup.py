from setuptools import setup

APP = ["one-api-menubar.py"]
DATA_FILES = []
OPTIONS = {
    "argv_emulation": False,
    "iconfile": None,
    "plist": {
        "CFBundleName": "one-api-menubar",
        "CFBundleIdentifier": "com.oneapi.menubar",
        "CFBundleShortVersionString": "0.1.0",
        "LSUIElement": True,
    },
    "packages": ["rumps"],
    "includes": ["oneapi_usage"],
}

setup(
    app=APP,
    data_files=DATA_FILES,
    options={"py2app": OPTIONS},
    setup_requires=["py2app"],
)
