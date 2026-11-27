---
title: Insights
description: 'How often you met your goals, your streaks, and how two measures move together.'
---

The **Insights** card has three tabs. Goals and Explore follow the range picked; Streaks looks at everything synced.

## Goals

How often each goal was met:

- **Protein** at or above its goal.
- **Calories** by phase: in a cut at or below the goal, in a bulk at or above it, otherwise within ±10 % (the goal including the day's activity).
- **Steps** and the **sleep goal**.
- **Days without alcohol**, once alcohol was ever logged.

Days without data don't count and today is left out. The weekly review gets the same counts for its week.

![Insights → Goals](../../../assets/screens/card-insights.png)

## Streaks

For food logged and each goal above, one line:

- **Days in a row** the goal was met, up to yesterday. It reads "Your longest yet" when no earlier streak was longer.
- **Longest** streak so far, with its first and last day.
- **Last missed**, and how many days ago. For alcohol that's the last day with a drink and its grams; for food logged, the last day without an entry.

A day without data ends a streak. Today is still running and left out, except a drink logged today, which ends the alcohol-free streak at once. Every date opens its day.

Below the streaks, **Last time**:

- **Last training**: how many days ago, the day's workouts, and how many days you trained in the last 4 weeks. It counts what the training calendar counts: Fitbit workouts of your training types (Profile → Training; without a choice, all but walks) and days with a lift, benchmark result or training noted.
- **Last weigh-in**: how many days ago, and the weight.

Both include today.

## Explore

Pick any two daily measures from nutrition (macros, alcohol, fibre, sodium, magnesium), movement and training load, sleep (duration, deep, REM, bedtime, wake time, mid-sleep), recovery, the scores, the check-in and the weight trend. Then pick when the second one is read: the same day, the day after or two days after. Sleep belongs to the morning it ended, so "the day after" a day is the night that followed.

![Insights → Explore](../../../assets/screens/card-insights-explore.png)

A scatter plot shows one dot per day with a trend line, Spearman's r (−1 to 1, in words from "no relation" to "strong"), the number of days and the period. Hover a dot for its values; click it to open the day. Sick and travel days can be left out.

It needs at least 10 days with both, and says plainly that it shows a pattern, not proof. The **Patterns** tab of Sleep & Recovery links to it.
