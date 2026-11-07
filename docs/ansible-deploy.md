# Deploy do Artex com Ansible

Sobe o Artex na máquina `ex-machina` (`192.168.33.11`). Cada execução puxa o `main`, constrói a imagem local e recria os containers.

Rode na raiz deste repositório, na máquina que alcança a `ex-machina` por SSH:

```bash
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/artex.yml
```

O Ansible precisa estar instalado nessa máquina de controle, não na `ex-machina`. O pacote é o `ansible-core`. A conta `user` em `192.168.33.11` precisa entrar por SSH sem senha e já pertence ao grupo `docker`.

## O que a execução faz

1. Clona `https://github.com/ericssondelima/artex-x-ollama.git` em `/home/user/artex-x-ollama` se o diretório ainda não existir.
2. Faz `git fetch` e `git reset --hard origin/main`. Alterações locais em arquivos versionados nessa cópia são descartadas.
3. Grava `.compose.runtime.yml`. Esse arquivo troca a imagem `autumn27/artex` (não publicada) por `artex:local` e aponta o container para o Ollama do host em `host.docker.internal:11434`.
4. Cria o `.env` só na primeira vez, com senha aleatória do Postgres, provider `ollama` e modelo `qwen2.5-coder:7b`. Um `.env` que já existe permanece como está.
5. Garante o volume Docker `artex_pgdata`.
6. Roda `docker compose up -d --build --force-recreate`. O `docker-compose.override.yml` do Git não entra nesse comando: ele muda a porta para outra máquina.
7. Espera `http://127.0.0.1:8787/` responder dentro do host.

## Resultado

| | |
| --- | --- |
| Console | http://192.168.33.11:8787 |
| Proxy de tráfego | `127.0.0.1:8788` só na `ex-machina` |
| Ollama | o daemon da própria máquina, porta `11434` |
| Modelo | `qwen2.5-coder:7b` |

Na primeira abertura do console, `/setup` pede a senha de administrador.

## Arquivos

| Arquivo | Função |
| --- | --- |
| `deploy/ansible/artex.yml` | Playbook |
| `deploy/ansible/inventory.ini` | Host `ex-machina`, usuário `user` |
| `deploy/ansible/templates/compose.runtime.yml.j2` | Modelo do Compose de runtime |
