# Maestro Demo App Spec

## Purpose

This project is a reusable autonomy testbed for Maestro. It should be small enough to complete in a few workflow runs, but concrete enough that issue quality, agent-pack design, workflow routing, and operator controls can be evaluated against real deliverables.

## Product

Build a simple task-tracking web app with:

- a backend API
- a frontend UI
- persistent storage
- automated verification

The app should support one local user and run entirely on a developer machine without external hosted services.

## Core user story

As a developer, I can:

- view a list of tasks
- create a new task
- edit a task title and description
- mark a task complete
- keep tasks after restarting the app

## Non-goals for the first iteration

- authentication
- multi-user collaboration
- background jobs
- notifications
- complex permissions
- cloud deployment

## Delivery expectations

- The repo should be runnable from a clean checkout.
- The stack should be intentionally simple and documented.
- The project should include a repeatable verification path.
- The issues should be explicit enough that most runs can happen autonomously.
- If an issue is missing information needed to complete the work safely, the agent should ask a focused question through Maestro or document the blocker and stop rather than guessing.

## Definition of done

The first complete pass is done when:

1. the repo has a documented app shell
2. task CRUD works through the UI
3. data survives restart
4. there is a repeatable local verification command
5. CI runs the key checks

## Backlog design rules

Each issue should include:

- goal
- exact deliverables
- acceptance criteria
- verification steps
- constraints
- dependencies if any

Each issue should avoid:

- vague “improve” language
- broad refactors
- hidden requirements
- unstated verification expectations

## Ambiguity handling

Autonomy does not mean guessing. For this testbed:

- issues should be specific enough that a well-configured workflow usually finishes without help
- if a workflow still lacks required information, the agent should ask a focused question through Maestro controls
- if the missing information cannot be resolved quickly, the run should document the blocker clearly in the issue context and stop without pretending the work is complete
