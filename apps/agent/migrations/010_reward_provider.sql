-- Reward payouts arrive with a blank `provider_id`, so device-scoped queries could
-- never match them and the analysis page silently dropped them. Tag existing rows
-- with the sentinel used by new rounds; rows that already carry a Provider keep it.
UPDATE earnings
   SET provider_id = 'bloomer_reward'
 WHERE trim(provider_id) = ''
   AND replace(replace(replace(replace(lower(trim(model)), '-', ''), '_', ''), '.', ''), ' ', '')
       IN ('basereward', 'basebonus', 'bonus', 'reward', 'subsidy', 'incentive');
