# Interactive Code Note
[![image](https://github.com/user-attachments/assets/23ae7fc9-dcf5-4bba-9d16-f0bab70cc6dc)](https://blog.piger.tech/posts/2024/07/test/)
## Introduction

Interactive Code Note is a web-based code editor that supports multiple programming languages. You can run code snippets, save versions, and retrieve previous versions. This document provides instructions for setting up and running the project.

## Setup Instructions

### Step 1: Initialize the Backend

Navigate to the backend directory and initialize the Go module.

```sh
cd backend
go mod init InteractiveCodeNote
go get Interactive_note
```
### Step 2: Modify Configuration
Edit the `config.yaml` file to configure the server settings.  

### Step 3: Parse Configuration  
Run the `parse_config.sh` script to read the configuration variables.
```sh
chmod +x parse_config.sh
./parse_config.sh
```
### Step 4: Run with Docker  
```sh
cd ..
docker-compose down
docker-compose build
docker-compose up -d
```
Then your can see your Interactive code block on `http(s)//:your domain:port`  
If u want to insert blocks on pages like the [demo](https://blog.piger.tech/posts/2024/07/test/). 
Follow steps below:
### Step 5: Insert on Markdown  
Change  `path/to/your/index.html`. `yourcodeid' is the name of your code block.
```html
<iframe id="go-editor-1" src="path/to/your/index.html?noteId=yourcodeid" style="width:100%; height:500px;
border:none;" frameborder="0"></iframe>
```

