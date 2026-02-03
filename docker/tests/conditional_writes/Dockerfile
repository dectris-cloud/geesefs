# Dockerfile for conditional write test container
FROM python:3.12-alpine

# Install required packages
RUN pip install --no-cache-dir boto3

# Create app directory
WORKDIR /app

# Copy test script
COPY test_conditional_writes.py /app/

CMD ["python3", "/app/test_conditional_writes.py"]
