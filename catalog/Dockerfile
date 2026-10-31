FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py vlcrc.py cdimage.py musicbrainz.py .
COPY static ./static

ENV PYTHONUNBUFFERED=1
EXPOSE 8080

# One worker: the catalog lives in memory and is shared between threads.
CMD ["gunicorn", "-b", "0.0.0.0:8080", "-w", "1", "--threads", "8", "app:app"]
