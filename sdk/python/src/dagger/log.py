import logging
import logging.config


def configure_logging(level: int | str = logging.WARNING):
    """Configure logging for the dagger package.

    Sets a console handler with a simple format and defaults to WARNING level,
    but can be set to DEBUG to see more information.
    """
    config = {
        "version": 1,
        "disable_existing_loggers": False,
        "formatters": {
            "simple": {"format": "[{levelname}] {name}: {message}", "style": "{"},
        },
        "handlers": {
            "console": {
                "level": "DEBUG",
                "class": "logging.StreamHandler",
                "formatter": "simple",
            },
        },
        "loggers": {
            "dagger": {
                "handlers": ["console"],
                "level": level,
            },
        },
    }
    logging.config.dictConfig(config)


def configure_debug_logging():
    """Configure logging for the dagger package with DEBUG level."""
    configure_logging(logging.DEBUG)
