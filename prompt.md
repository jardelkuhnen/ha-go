Repositório: /workspace/home-assistent-go
Specs: /workspace/home-assistent-go/docs/specs/

Quero que você implemente todas as spescs do diretório usando o seguinte processo:

1. Leia as specs e identifique dependências entre elas (se alguma spec exigir que
   outra já esteja implementada). Se não estiver explícito no conteúdo, me pergunte
   antes de continuar.

2. Monte as ondas de execução por ordenação topológica das dependências.
   Dentro da mesma onda, specs independentes rodam em paralelo, cada uma no seu
   próprio worktree (Agent com isolation: "worktree"). Specs de ondas diferentes
   rodam em sequência.

3. Para cada spec, dentro do seu worktree isolado:
   a. Use a skill superpowers:writing-plans para transformar o conteúdo da spec
      em um plano com tarefas discretas e Global Constraints.
   b. Use a skill superpowers:subagent-driven-development para executar esse
      plano: implementer por tarefa, task review, fix loop quando necessário,
      revisão final da branch inteira.
   c. Branch da spec:
      - sem dependência → nasce de main
      - com dependência → nasce do commit revisado mais recente da branch da
        spec da qual depende (não de main)
      - Para cada spec realize os commits no local, sem realizar push.

4. Mantenha /workspace/home-assistent-go/.loop-ledger.md com: spec, onda, depende-de, branch,
   status (pending/in_progress/review/done/blocked). Se uma spec falhar, marque
   como blocked as specs que dependem dela e não as inicie.

5. Ao final de cada spec, use superpowers:finishing-a-development-branch para
   decidir o destino da branch — NÃO faça merge nem push de nenhuma branch sem
   minha confirmação explícita.

6. Ao final da implementação de cada branch, utilize o /code-review para validar a implementação realizada e garantir que nao temos bugs, vulnerabilidades, ou algum problema de performance. 

Antes de começar, me mostre o grafo de dependências e as ondas que você
identificou, para eu confirmar, e só então dispare os agentes.