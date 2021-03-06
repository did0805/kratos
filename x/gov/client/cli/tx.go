package cli

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/KuChainNetwork/kuchain/chain/client/flags"
	"github.com/KuChainNetwork/kuchain/chain/client/txutil"
	chainTypes "github.com/KuChainNetwork/kuchain/chain/types"
	govutils "github.com/KuChainNetwork/kuchain/x/gov/client/utils"
	"github.com/KuChainNetwork/kuchain/x/gov/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/spf13/cobra"
)

// Proposal flags
const (
	FlagTitle        = "title"
	FlagDescription  = "description"
	flagProposalType = "type"
	FlagDeposit      = "deposit"
	flagVoter        = "voter"
	flagDepositor    = "depositor"
	flagStatus       = "status"
	FlagProposal     = "proposal"
)

type proposal struct {
	Title       string
	Description string
	Type        string
	Deposit     string
}

// ProposalFlags defines the core required fields of a proposal. It is used to
// verify that these values are not provided in conjunction with a JSON proposal
// file.
var ProposalFlags = []string{
	FlagTitle,
	FlagDescription,
	flagProposalType,
	FlagDeposit,
}

// GetTxCmd returns the transaction commands for this module
// governance ModuleClient is slightly different from other ModuleClients in that
// it contains a slice of "proposal" child commands. These commands are respective
// to proposal type handlers that are implemented in other modules but are mounted
// under the governance CLI (eg. parameter change proposals).
func GetTxCmd(storeKey string, cdc *codec.Codec, pcmds []*cobra.Command) *cobra.Command {
	govTxCmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Governance transactions subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmdSubmitProp := GetCmdSubmitProposal(cdc)
	for _, pcmd := range pcmds {
		cmdSubmitProp.AddCommand(flags.PostCommands(pcmd)[0])
	}

	govTxCmd.AddCommand(flags.PostCommands(
		GetCmdDeposit(cdc),
		GetCmdVote(cdc),
		GetCmdUnJail(cdc),
		cmdSubmitProp,
	)...)

	return govTxCmd
}

// GetCmdSubmitProposal implements submitting a proposal transaction command.
func GetCmdSubmitProposal(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-proposal [proposer]",
		Short: "Submit a proposal along with an initial deposit",
		Args:  cobra.ExactArgs(1),
		Long: strings.TrimSpace(
			fmt.Sprintf(`Submit a proposal along with an initial deposit.
Proposal title, description, type and deposit can be given directly or through a proposal JSON file.

Example:
$ %s tx kugov submit-proposal jack --proposal="path/to/proposal.json" --from jack

Where proposal.json contains:

{
  "title": "Test Proposal",
  "description": "My awesome proposal",
  "type": "Text",
  "deposit": "10test"
}

Which is equivalent to:

$ %s tx kugov submit-proposal jack --title="Test Proposal" --description="My awesome proposal" --type="Text" --deposit="10test" --from jack
`,
				version.ClientName, version.ClientName,
			),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := txutil.NewTxBuilderFromCLI(inBuf).WithTxEncoder(txutil.GetTxEncoder(cdc))
			cliCtx := txutil.NewKuCLICtxByBuf(cdc, inBuf)

			proposal, err := parseSubmitProposalFlags()
			if err != nil {
				return err
			}

			amount, err := chainTypes.ParseCoins(proposal.Deposit)
			if err != nil {
				return err
			}

			proposerAccount, err := chainTypes.NewAccountIDFromStr(args[0])
			if err != nil {
				return sdkerrors.Wrap(err, "proposer account id error")
			}

			content := types.ContentFromProposalType(proposal.Title, proposal.Description, proposal.Type)

			proposalAccAddress, err := txutil.QueryAccountAuth(cliCtx, proposerAccount)
			if err != nil {
				return sdkerrors.Wrapf(err, "query account %s auth error", proposerAccount)
			}

			msg := types.NewKuMsgSubmitProposal(proposalAccAddress, content, amount, proposerAccount)
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			cliCtx = cliCtx.WithFromAccount(proposerAccount)
			if txBldr.FeePayer().Empty() {
				txBldr = txBldr.WithPayer(args[0])
			}
			return txutil.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}

	cmd.Flags().String(FlagTitle, "", "title of proposal")
	cmd.Flags().String(FlagDescription, "", "description of proposal")
	cmd.Flags().String(flagProposalType, "", "proposalType of proposal, types: text/parameter_change/software_upgrade")
	cmd.Flags().String(FlagDeposit, "", "deposit of proposal")
	cmd.Flags().String(FlagProposal, "", "proposal file path (if this path is given, other proposal flags are ignored)")

	return cmd
}

// GetCmdDeposit implements depositing tokens for an active proposal.
func GetCmdDeposit(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "deposit [depositor] [proposal-id] [deposit]",
		Args:  cobra.ExactArgs(3),
		Short: "Deposit tokens for an active proposal",
		Long: strings.TrimSpace(
			fmt.Sprintf(`Submit a deposit for an active proposal. You can
find the proposal-id by running "%s query gov proposals".

Example:
$ %s tx kugov deposit 1 10stake --from mykey
`,
				version.ClientName, version.ClientName,
			),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := txutil.NewTxBuilderFromCLI(inBuf).WithTxEncoder(txutil.GetTxEncoder(cdc))
			cliCtx := txutil.NewKuCLICtxByBuf(cdc, inBuf)

			// validate that the proposal id is a uint
			proposalID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("proposal-id %s not a valid uint, please input a valid proposal-id", args[1])
			}

			// Get amount of coins
			amount, err := chainTypes.ParseCoins(args[2])
			if err != nil {
				return err
			}

			depositorAccount, err := chainTypes.NewAccountIDFromStr(args[0])
			if err != nil {
				return sdkerrors.Wrap(err, "depositor account id error")
			}

			// Get depositor address
			depositorAccAddress, err := txutil.QueryAccountAuth(cliCtx, depositorAccount)
			if err != nil {
				return sdkerrors.Wrapf(err, "query account %s auth error", depositorAccount)
			}

			msg := types.NewKuMsgDeposit(depositorAccAddress, depositorAccount, proposalID, amount)
			err = msg.ValidateBasic()
			if err != nil {
				return err
			}
			cliCtx = cliCtx.WithFromAccount(depositorAccount)
			if txBldr.FeePayer().Empty() {
				txBldr = txBldr.WithPayer(args[0])
			}
			return txutil.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdVote implements creating a new vote command.
func GetCmdVote(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "vote [voter-account] [proposal-id] [option]",
		Args:  cobra.ExactArgs(3),
		Short: "Vote for an active proposal, options: yes/no/no_with_veto/abstain",
		Long: strings.TrimSpace(
			fmt.Sprintf(`Submit a vote for an active proposal. You can
find the proposal-id by running "%s query gov proposals".


Example:
$ %s tx kugov vote jack 1 yes --from mykey
`,
				version.ClientName, version.ClientName,
			),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := txutil.NewTxBuilderFromCLI(inBuf).WithTxEncoder(txutil.GetTxEncoder(cdc))
			cliCtx := txutil.NewKuCLICtxByBuf(cdc, inBuf)

			// validate that the proposal id is a uint
			proposalID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("proposal-id %s not a valid int, please input a valid proposal-id", args[1])
			}

			// Find out which vote option user chose
			byteVoteOption, err := types.VoteOptionFromString(govutils.NormalizeVoteOption(args[2]))
			if err != nil {
				return err
			}

			VoterAccount, err := chainTypes.NewAccountIDFromStr(args[0])
			if err != nil {
				return sdkerrors.Wrap(err, "depositor account id error")
			}
			// Get vote address
			voterAccAddress, err := txutil.QueryAccountAuth(cliCtx, VoterAccount)
			if err != nil {
				return sdkerrors.Wrapf(err, "query account %s auth error", VoterAccount)
			}
			// Build vote message and run basic validation
			msg := types.NewKuMsgVote(voterAccAddress, VoterAccount, proposalID, byteVoteOption)
			err = msg.ValidateBasic()
			if err != nil {
				return err
			}
			cliCtx = cliCtx.WithFromAccount(VoterAccount)
			if txBldr.FeePayer().Empty() {
				txBldr = txBldr.WithPayer(args[0])
			}
			return txutil.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdVote implements creating a new vote command.
func GetCmdUnJail(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "unjail [validator-account]",
		Args:  cobra.ExactArgs(1),
		Short: "unjail validator previously jailed for downtime",
		Long: `unjail a jailed validator:

$ <appcli> tx kugov unjail validator --from validator
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := txutil.NewTxBuilderFromCLI(inBuf).WithTxEncoder(txutil.GetTxEncoder(cdc))
			cliCtx := txutil.NewKuCLICtxByBuf(cdc, inBuf)

			ValidatorAccount, err := chainTypes.NewAccountIDFromStr(args[0])
			if err != nil {
				return sdkerrors.Wrap(err, "depositor account id error")
			}
			// Get unjail address
			ValidatorAccAddress, err := txutil.QueryAccountAuth(cliCtx, ValidatorAccount)
			if err != nil {
				return sdkerrors.Wrapf(err, "query account %s auth error", ValidatorAccount)
			}
			// Build unjail message and run basic validation
			msg := types.NewMsgGovUnjail(ValidatorAccAddress, ValidatorAccount)
			err = msg.ValidateBasic()
			if err != nil {
				return err
			}
			cliCtx = cliCtx.WithFromAccount(ValidatorAccount)
			if txBldr.FeePayer().Empty() {
				txBldr = txBldr.WithPayer(args[0])
			}
			return txutil.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// DONTCOVER
