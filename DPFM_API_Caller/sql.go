package dpfm_api_caller

import (
	"context"
	dpfm_api_input_reader "data-platform-api-attendance-creates-rmq-kube/DPFM_API_Input_Reader"
	dpfm_api_output_formatter "data-platform-api-attendance-creates-rmq-kube/DPFM_API_Output_Formatter"
	dpfm_api_processing_formatter "data-platform-api-attendance-creates-rmq-kube/DPFM_API_Processing_Formatter"
	"data-platform-api-attendance-creates-rmq-kube/sub_func_complementer"
	"fmt"
	"sync"

	"github.com/latonaio/golang-logging-library-for-data-platform/logger"
	"golang.org/x/xerrors"
)

func (c *DPFMAPICaller) createSqlProcess(
	ctx context.Context,
	mtx *sync.Mutex,
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	subfuncSDC *sub_func_complementer.SDC,
	accepter []string,
	errs *[]error,
	log *logger.Logger,
) interface{} {
	var header *dpfm_api_output_formatter.Header

	//subfuncSDC.Message.Header = input.Header

	for _, fn := range accepter {
		switch fn {
		case "Header":
			var calculateAttendanceQueryGets *sub_func_complementer.CalculateAttendanceQueryGets
			var attendanceIssuedID int

			calculateAttendanceQueryGets = c.CalculateAttendance(errs)

			if calculateAttendanceQueryGets == nil {
				err := xerrors.Errorf("calculateAttendanceQueryGets is nil")
				*errs = append(*errs, err)
				return nil
			}

			attendanceIssuedID = calculateAttendanceQueryGets.AttendanceLatestNumber + 1

			input.Header.Attendance = &attendanceIssuedID

			header = c.headerCreateSql(nil, mtx, input, output, subfuncSDC, errs, log)

			if calculateAttendanceQueryGets != nil {
				err := c.UpdateLatestNumber(errs, attendanceIssuedID)
				if err != nil {
					*errs = append(*errs, err)
					return nil
				}
			}
		default:
		}
	}

	data := &dpfm_api_output_formatter.Message{
		Header: header,
	}

	return data
}

func (c *DPFMAPICaller) updateSqlProcess(
	ctx context.Context,
	mtx *sync.Mutex,
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	accepter []string,
	errs *[]error,
	log *logger.Logger,
) interface{} {
	var header *dpfm_api_output_formatter.Header
	for _, fn := range accepter {
		switch fn {
		case "Header":
			header = c.headerUpdateSql(mtx, input, output, errs, log)
		default:

		}
	}

	data := &dpfm_api_output_formatter.Message{
		Header: header,
	}

	return data
}

func (c *DPFMAPICaller) headerCreateSql(
	ctx context.Context,
	mtx *sync.Mutex,
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	subfuncSDC *sub_func_complementer.SDC,
	errs *[]error,
	log *logger.Logger,
) *dpfm_api_output_formatter.Header {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID := input.RuntimeSessionID

	dpfm_api_output_formatter.ConvertToHeader(input, subfuncSDC)

	headerData := subfuncSDC.Message.Header
	res, err := c.rmq.SessionKeepRequest(ctx, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{"message": headerData, "function": "AttendanceHeader", "runtime_session_id": sessionID})
	if err != nil {
		err = xerrors.Errorf("rmq error: %w", err)
		*errs = append(*errs, err)
		return nil
	}
	res.Success()
	if !checkResult(res) {
		output.SQLUpdateResult = getBoolPtr(false)
		output.SQLUpdateError = "Header Data cannot insert"
		return nil
	}

	if output.SQLUpdateResult == nil {
		output.SQLUpdateResult = getBoolPtr(true)
	}

	data, err := dpfm_api_output_formatter.ConvertToHeaderCreates(subfuncSDC)
	if err != nil {
		*errs = append(*errs, err)
		return nil
	}

	return data
}

func (c *DPFMAPICaller) headerUpdateSql(
	mtx *sync.Mutex,
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	errs *[]error,
	log *logger.Logger,
) *dpfm_api_output_formatter.Header {
	header := input.Header
	headerData := dpfm_api_processing_formatter.ConvertToHeaderUpdates(header)

	sessionID := input.RuntimeSessionID
	if headerIsUpdate(headerData) {
		res, err := c.rmq.SessionKeepRequest(nil, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{"message": headerData, "function": "AttendanceHeader", "runtime_session_id": sessionID})
		if err != nil {
			err = xerrors.Errorf("rmq error: %w", err)
			*errs = append(*errs, err)
			return nil
		}
		res.Success()
		if !checkResult(res) {
			output.SQLUpdateResult = getBoolPtr(false)
			output.SQLUpdateError = "Header Data cannot update"
			return nil
		}
	}

	if output.SQLUpdateResult == nil {
		output.SQLUpdateResult = getBoolPtr(true)
	}

	data, err := dpfm_api_output_formatter.ConvertToHeaderUpdates(header)
	if err != nil {
		*errs = append(*errs, err)
		return nil
	}

	return data
}

func headerIsUpdate(header *dpfm_api_processing_formatter.HeaderUpdates) bool {
	attendance := header.Attendance

	return !(attendance == 0)
}

func (c *DPFMAPICaller) CalculateAttendance(
	errs *[]error,
) *sub_func_complementer.CalculateAttendanceQueryGets {
	pm := &sub_func_complementer.CalculateAttendanceQueryGets{}

	rows, err := c.db.Query(
		`SELECT *
		FROM DataPlatformMastersAndTransactionsMysqlKube.data_platform_number_range_latest_number_data
		WHERE (ServiceLabel, FieldNameWithNumberRange) = (?, ?);`, "PARTICIPATION", "Attendance",
	)
	if err != nil {
		*errs = append(*errs, err)
		return nil
	}

	for i := 0; true; i++ {
		if !rows.Next() {
			if i == 0 {
				*errs = append(*errs, fmt.Errorf("'data_platform_number_range_latest_number_data'テーブルに対象のレコードが存在しません。"))
				return nil
			} else {
				break
			}
		}
		err = rows.Scan(
			&pm.NumberRangeID,
			&pm.ServiceLabel,
			&pm.FieldNameWithNumberRange,
			&pm.AttendanceLatestNumber,
		)
		if err != nil {
			*errs = append(*errs, err)
			return nil
		}
	}

	return pm
}

func (c *DPFMAPICaller) UpdateLatestNumber(
	errs *[]error,
	attendanceIssuedID int,
) error {
	//rows, err := c.db.Query(
	//	`SELECT *
	//	FROM DataPlatformMastersAndTransactionsMysqlKube.data_platform_number_range_latest_number_data
	//	WHERE (ServiceLabel, FieldNameWithNumberRange) = (?, ?);`, "ORDERS", "Attendance",
	//)

	_, err := c.db.Exec(`
			UPDATE data_platform_number_range_latest_number_data SET LatestNumber=(?)
			WHERE (ServiceLabel, FieldNameWithNumberRange) = (?, ?);`,
		attendanceIssuedID,
		"ATTENDANCE",
		"Attendance",
	)
	if err != nil {
		return xerrors.Errorf("'data_platform_number_range_latest_number_data'テーブルの更新に失敗しました。")
	}

	return nil
}
